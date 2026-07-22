// Command fake-mps is a drop-in stand-in for nvidia-cuda-mps-control, used on
// fake-GPU E2E clusters where there is no real GPU/driver.
//
// It does exactly enough to satisfy the mpsd supervisor and its readiness probe:
//   - creates a UNIX socket at $CUDA_MPS_PIPE_DIRECTORY/control so the readiness
//     probe (`test -S .../control`) passes, and
//   - stays in the foreground until the supervisor asks it to stop by writing
//     "quit" to stdin (the same mechanism the real binary uses) or closes stdin
//     (EOF), or until it receives SIGTERM/SIGINT.
//
// It never touches a GPU. The mpsd supervisor invokes it with the real flags
// (e.g. `-p 3 -f -a <config>`); they are all intentionally ignored.
package main

import (
	"bufio"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

func main() {
	pipeDir := os.Getenv("CUDA_MPS_PIPE_DIRECTORY")
	if pipeDir == "" {
		pipeDir = "/run/nvidia-mps"
	}
	if err := os.MkdirAll(pipeDir, 0o755); err != nil {
		log.Fatalf("fake-mps: mkdir %s: %v", pipeDir, err)
	}

	sock := filepath.Join(pipeDir, "control")
	_ = os.Remove(sock) // clear any stale socket before binding

	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatalf("fake-mps: listen %s: %v", sock, err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(sock)
	}()
	log.Printf("fake-mps: control socket ready at %s", sock)

	// Accept and immediately close any client connections so the path stays a
	// live listening socket (and never blocks a would-be MPS client).
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	// The supervisor stops us by writing "quit" to stdin, or by closing stdin
	// (EOF) — mirror the real nvidia-cuda-mps-control contract.
	quit := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "quit" {
				break
			}
		}
		close(quit)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	select {
	case <-quit:
		log.Printf("fake-mps: received quit, shutting down")
	case s := <-sig:
		log.Printf("fake-mps: received %s, shutting down", s)
	}
}
