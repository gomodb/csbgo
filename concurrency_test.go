package csbgo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestClientConcurrentUse proves a single Client may be initialized once and
// shared across many goroutines (the package-level-variable pattern), as well
// as a single Request template being cloned per call. Run with -race to catch
// any data race.
func TestClientConcurrentUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	// The shared, package-level-style client and request template.
	client := New(WithAK("ak"), WithSK("sk"), WithBaseURL(srv.URL))
	template := NewRequest(MethodGet).
		WithAPI("PING").
		WithVersion("vcsb").
		Path("CSB")

	const workers = 64

	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			req := template.Clone().WithQueryInt("i", n)

			resp, err := client.Do(context.Background(), req)
			if err != nil {
				errs <- fmt.Errorf("worker %d: %w", n, err)
				return
			}

			if resp.ToString() != "ok" || !resp.OK() {
				errs <- fmt.Errorf("worker %d: bad response %q", n, resp.ToString())
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}
