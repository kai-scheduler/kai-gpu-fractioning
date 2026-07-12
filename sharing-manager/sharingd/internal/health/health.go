// Package health exposes sharingd's NRI registration state as an HTTP
// readiness endpoint. sharingd retries its NRI connection indefinitely by
// default, so without this signal a pod that never registers with containerd
// still reports Ready and the operator marks the node healthy. The kubelet
// probes /readyz; the plugin flips the shared State on Synchronize (registered
// and synced) and back off when the connection drops.
package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	// ReadyzPath is the HTTP path served by the readiness server, probed by
	// the kubelet readiness probe configured by the operator.
	ReadyzPath = "/readyz"

	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// State is a concurrency-safe readiness flag. The NRI plugin writes it from
// stub callbacks; the HTTP server reads it on every probe.
type State struct {
	ready atomic.Bool
}

// SetReady records whether the NRI plugin is registered and synchronized.
func (s *State) SetReady(ready bool) {
	s.ready.Store(ready)
}

// Ready reports the last recorded readiness.
func (s *State) Ready() bool {
	return s.ready.Load()
}

// Handler returns the readiness HTTP handler: 200 when state is ready,
// 503 otherwise.
func Handler(state *State) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(ReadyzPath, func(w http.ResponseWriter, _ *http.Request) {
		if !state.Ready() {
			http.Error(w, "not ready: NRI plugin not registered", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// Serve runs the readiness HTTP server on port until ctx is cancelled, then
// shuts it down gracefully. It returns only on listener failure or after
// shutdown completes.
func Serve(ctx context.Context, port int, state *State, log *slog.Logger) error {
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           Handler(state),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("starting readiness server", "addr", server.Addr, "path", ReadyzPath)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("readiness server: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
