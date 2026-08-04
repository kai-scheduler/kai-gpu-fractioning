// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestCRIStopperStopsContainer(t *testing.T) {
	fake := &fakeRuntimeService{}
	s, dials := bufconnStopper(t, fake, 7*time.Second)

	if err := s.Stop(context.Background(), "c123"); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	if got := fake.calls(); got != 1 {
		t.Fatalf("StopContainer calls = %d, want 1", got)
	}
	if got := fake.lastID(); got != "c123" {
		t.Fatalf("stopped container ID = %q, want c123", got)
	}
	// Timeout is passed to the runtime in whole seconds.
	if got := fake.lastTimeout(); got != 7 {
		t.Fatalf("stop timeout = %d, want 7", got)
	}
	if *dials != 1 {
		t.Fatalf("dial count = %d, want 1", *dials)
	}
}

func TestCRIStopperReusesConnection(t *testing.T) {
	fake := &fakeRuntimeService{}
	s, dials := bufconnStopper(t, fake, time.Second)

	for _, id := range []string{"a", "b", "c"} {
		if err := s.Stop(context.Background(), id); err != nil {
			t.Fatalf("Stop(%s) returned error: %v", id, err)
		}
	}

	if got := fake.calls(); got != 3 {
		t.Fatalf("StopContainer calls = %d, want 3", got)
	}
	// The connection must be dialed once and reused across stops.
	if *dials != 1 {
		t.Fatalf("dial count = %d, want 1", *dials)
	}
}

func TestCRIStopperPropagatesRuntimeError(t *testing.T) {
	fake := &fakeRuntimeService{err: errors.New("boom")}
	s, _ := bufconnStopper(t, fake, time.Second)

	err := s.Stop(context.Background(), "c1")
	if err == nil {
		t.Fatal("expected error when runtime StopContainer fails, got nil")
	}
}

func TestCRIStopperCloseWithoutDial(t *testing.T) {
	s := NewCRIStopper("/run/containerd/containerd.sock", time.Second, nil)
	if err := s.Close(); err != nil {
		t.Fatalf("Close on never-dialed stopper: %v", err)
	}
}

func TestNewCRIStopperDefaults(t *testing.T) {
	s := NewCRIStopper("/sock", 0, nil)
	if s.timeout != defaultCRIStopTimeout {
		t.Fatalf("timeout = %v, want default %v", s.timeout, defaultCRIStopTimeout)
	}
	if s.log == nil {
		t.Fatal("log should default to a non-nil logger")
	}
}

// bufconnStopper wires a CRIStopper to an in-memory gRPC server running svc, so
// tests exercise the real StopContainer client path without a runtime socket. It
// returns the stopper and a pointer to the dial counter.
func bufconnStopper(t *testing.T, svc runtimeapi.RuntimeServiceServer, timeout time.Duration) (*CRIStopper, *int) {
	t.Helper()

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	runtimeapi.RegisterRuntimeServiceServer(srv, svc)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	dials := new(int)
	dial := func(string) (runtimeapi.RuntimeServiceClient, *grpc.ClientConn, error) {
		*dials++
		conn, err := grpc.NewClient(
			"passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return nil, nil, err
		}
		return runtimeapi.NewRuntimeServiceClient(conn), conn, nil
	}
	s := NewCRIStopper("bufnet", timeout, nil, WithDial(dial))
	t.Cleanup(func() { _ = s.Close() })
	return s, dials
}

// fakeRuntimeService is a minimal CRI RuntimeService that records StopContainer
// calls and optionally returns a canned error.
type fakeRuntimeService struct {
	runtimeapi.UnimplementedRuntimeServiceServer

	mu          sync.Mutex
	stopCalls   int
	stoppedID   string
	stopTimeout int64
	err         error
}

func (f *fakeRuntimeService) StopContainer(_ context.Context, req *runtimeapi.StopContainerRequest) (*runtimeapi.StopContainerResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	f.stoppedID = req.ContainerId
	f.stopTimeout = req.Timeout
	if f.err != nil {
		return nil, f.err
	}
	return &runtimeapi.StopContainerResponse{}, nil
}

func (f *fakeRuntimeService) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCalls
}

func (f *fakeRuntimeService) lastID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stoppedID
}

func (f *fakeRuntimeService) lastTimeout() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopTimeout
}
