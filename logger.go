package csbgo

import (
	"fmt"
	"os"
	"sync"
)

// Logger is the debug-logging sink used by Client when debug mode is enabled.
// It is satisfied by *log.Logger (its Printf method matches) and by any
// custom implementation, which lets you keep CSB logs in your own logger.
type Logger interface {
	Printf(format string, v ...any)
}

// defaultLogger writes to stderr and is safe for concurrent use. It is only
// used when no logger is supplied via WithLogger.
type defaultLogger struct {
	mu sync.Mutex
}

func (l *defaultLogger) Printf(format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	fmt.Fprintf(os.Stderr, "[csb] "+format+"\n", v...)
}
