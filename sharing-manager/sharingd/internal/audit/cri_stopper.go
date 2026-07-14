package audit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// defaultCRIStopTimeout is the grace period handed to the runtime when the
// caller does not configure one.
const defaultCRIStopTimeout = 30 * time.Second

// criStopSlack is added on top of the runtime grace period to bound the RPC, so
// a wedged runtime cannot block the remediation goroutine past its own timeout.
const criStopSlack = 5 * time.Second

// CRIStopper stops containers through the node's CRI runtime endpoint
// (containerd or CRI-O) via RuntimeService.StopContainer. It is the production
// ContainerStopper.
//
// The gRPC connection is dialed lazily on the first Stop and then reused. A dial
// or RPC failure is returned as an error (never a panic) so the remediator logs
// it and moves on to the next violation.
type CRIStopper struct {
	endpoint string        // CRI unix socket path, e.g. /run/containerd/containerd.sock
	timeout  time.Duration // grace period handed to the runtime per stop
	log      *slog.Logger

	mu     sync.Mutex
	conn   *grpc.ClientConn
	client runtimeapi.RuntimeServiceClient

	// dial connects to the runtime endpoint. Defaults to dialCRI (real unix
	// socket) and is overridden via WithDial in tests.
	dial dialFunc
}

// dialFunc opens a lazy client to the CRI runtime at endpoint.
type dialFunc func(endpoint string) (runtimeapi.RuntimeServiceClient, *grpc.ClientConn, error)

// Option customizes a CRIStopper at construction.
type Option func(*CRIStopper)

// WithDial overrides how the CRIStopper connects to the runtime. It exists for
// tests (e.g. an in-memory gRPC server); production uses the default
// unix-socket dial.
func WithDial(dial dialFunc) Option {
	return func(s *CRIStopper) { s.dial = dial }
}

// Compile-time assertion that CRIStopper satisfies the remediation contract.
var _ ContainerStopper = (*CRIStopper)(nil)

// NewCRIStopper builds a CRIStopper for the given CRI unix socket path. A
// non-positive timeout falls back to defaultCRIStopTimeout; a nil log falls back
// to slog.Default(). No connection is opened until the first Stop.
func NewCRIStopper(endpoint string, timeout time.Duration, log *slog.Logger, opts ...Option) *CRIStopper {
	if timeout <= 0 {
		timeout = defaultCRIStopTimeout
	}
	if log == nil {
		log = slog.Default()
	}
	s := &CRIStopper{
		endpoint: endpoint,
		timeout:  timeout,
		log:      log,
		dial:     dialCRI,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Stop stops the container via CRI StopContainer, giving the runtime up to the
// configured timeout to stop gracefully before it is killed. The runtime then
// leaves it to kubelet to recreate the container per the pod restart policy.
func (s *CRIStopper) Stop(ctx context.Context, containerID string) error {
	client, err := s.runtimeClient()
	if err != nil {
		return fmt.Errorf("connect to CRI runtime at %q: %w", s.endpoint, err)
	}

	callCtx, cancel := context.WithTimeout(ctx, s.timeout+criStopSlack)
	defer cancel()

	if _, err := client.StopContainer(callCtx, &runtimeapi.StopContainerRequest{
		ContainerId: containerID,
		Timeout:     int64(s.timeout.Seconds()),
	}); err != nil {
		return fmt.Errorf("CRI StopContainer %s: %w", containerID, err)
	}
	return nil
}

// Close releases the gRPC connection. It is safe to call on a stopper that never
// dialed and safe to call more than once.
func (s *CRIStopper) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn, s.client = nil, nil
	return err
}

// runtimeClient returns the cached RuntimeService client, dialing once on first
// use. The connection is lazy (grpc.NewClient does not block), so a runtime that
// is briefly unavailable surfaces as an RPC error at Stop time rather than here.
func (s *CRIStopper) runtimeClient() (runtimeapi.RuntimeServiceClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}

	client, conn, err := s.dial(s.endpoint)
	if err != nil {
		return nil, err
	}
	s.client, s.conn = client, conn
	return s.client, nil
}

// dialCRI opens a lazy gRPC client to the CRI runtime over its unix socket. The
// runtime endpoint is unauthenticated on the node, so insecure transport
// credentials are expected here.
func dialCRI(endpoint string) (runtimeapi.RuntimeServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(
		"unix://"+endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, err
	}
	return runtimeapi.NewRuntimeServiceClient(conn), conn, nil
}
