package csbgo_test

import (
	"context"
	"fmt"
	"log"

	"github.com/gomodb/csbgo"
)

// ExampleClient demonstrates the recommended way to configure a Client and
// invoke a CSB-published service.
func ExampleClient() {
	client := csbgo.New(
		csbgo.WithBaseURL("http://broker.example.com/CSB"), // the single CSB endpoint
		csbgo.WithAK("your-access-key"),
		csbgo.WithSK("your-secret-key"),
		csbgo.WithDebug(true),
	)

	resp, err := client.Do(context.Background(),
		csbgo.NewRequest(csbgo.MethodPost).
			WithAPI("MyService").
			WithVersion("v1").
			WithQuery("name", "wiseking").
			WithForm("p1", "dog"),
	)
	if err != nil {
		// Non-2xx statuses surface here as *csbgo.StatusError.
		log.Fatalf("call failed: %v", err)
	}

	fmt.Println(resp.ToString())
}

// ExampleClient_DoJSON shows a one-step request + JSON decode via Client.DoJSON.
func ExampleClient_DoJSON() {
	client := csbgo.New(
		csbgo.WithBaseURL("http://broker.example.com/CSB"),
		csbgo.WithAK("ak"),
		csbgo.WithSK("sk"),
	)

	var out map[string]any

	_, err := client.DoJSON(context.Background(),
		csbgo.NewRequest(csbgo.MethodPost).
			WithAPI("Echo").
			WithVersion("v1").
			WithJSON(map[string]any{"hello": "world"}),
		&out,
	)
	if err != nil {
		log.Fatalf("call failed: %v", err)
	}

	fmt.Printf("%v\n", out)
}

// ExampleClient_Do shows a JSON POST body through Client.Do.
func ExampleClient_Do() {
	client := csbgo.New(
		csbgo.WithBaseURL("http://broker.example.com/CSB"),
		csbgo.WithAK("ak"),
		csbgo.WithSK("sk"),
	)

	resp, err := client.Do(context.Background(),
		csbgo.NewRequest(csbgo.MethodPost).
			WithAPI("Echo").
			WithVersion("v1").
			WithJSON(map[string]any{"hello": "world"}),
	)
	if err != nil {
		log.Fatalf("call failed: %v", err)
	}

	var out map[string]any
	if err := resp.ToJSON(&out); err != nil {
		log.Fatalf("decode failed: %v", err)
	}

	fmt.Printf("%v\n", out)
}

// ExampleRequest_Clone shows deriving per-call requests from an endpoint
// template without mutating the original.
func ExampleRequest_Clone() {
	base := csbgo.NewRequest(csbgo.MethodGet).
		WithAPI("MyService").
		WithVersion("v1")

	user := base.Clone().Path("users/").Path("1001")
	orders := base.Clone().Path("orders/").WithQueryInt("page", 2)

	_ = user
	_ = orders
	// Pass user / orders to client.Do(ctx, ...) as needed.
}
