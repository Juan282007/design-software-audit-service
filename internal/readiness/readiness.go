// Package readiness supplies deployment-internal dependency checks. It is
// intentionally separate from the public liveness route defined in audit.yaml.
package readiness

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Checker struct {
	check    func(context.Context) error
	interval time.Duration
	ready    atomic.Bool
	stop     chan struct{}
	close    sync.Once
}

func New(ctx context.Context, interval time.Duration, check func(context.Context) error) *Checker {
	checker := &Checker{check: check, interval: interval, stop: make(chan struct{})}
	checker.refresh(ctx)
	go checker.watch()
	return checker
}

func (c *Checker) Ready() bool { return c.ready.Load() }

func (c *Checker) Close() { c.close.Do(func() { close(c.stop) }) }

func (c *Checker) refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, c.interval)
	defer cancel()
	c.ready.Store(c.check(ctx) == nil)
}

func (c *Checker) watch() {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.refresh(context.Background())
		}
	}
}

func StartInternalHTTP(address string, ready func() bool) (*http.Server, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: Handler(ready)}
	go func() { _ = server.Serve(listener) }()
	return server, nil
}

func Handler(ready func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}
