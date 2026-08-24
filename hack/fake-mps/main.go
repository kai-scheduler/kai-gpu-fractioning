// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Command fake-mps is a drop-in stand-in for nvidia-cuda-mps-control, used on
// fake-GPU E2E clusters where there is no real GPU/driver.
//
// It does exactly enough to satisfy the mpsd supervisor, its readiness probe,
// and fractiond's bind mounts:
//   - creates a UNIX socket at $CUDA_MPS_PIPE_DIRECTORY/control so the readiness
//     probe (`test -S .../control`) passes,
//   - creates <pipeDir>/<server>/default/control for every [servers.<server>]
//     section in the -a config, because fractiond bind-mounts that directory
//     into sm-sharing containers and a bind mount with a missing source fails
//     the container's creation outright, and
//   - stays in the foreground until the supervisor asks it to stop by writing
//     "quit" to stdin (the same mechanism the real binary uses) or closes stdin
//     (EOF), or until it receives SIGTERM/SIGINT.
//
// It never touches a GPU, and it does not implement the control protocol — a
// connecting client is accepted and immediately dropped, and commands such as
// `client list` are not simulated (a canned response would only assert what
// this file wrote). The remaining flags (-p, -m, -f) are ignored.
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

	sockets := []string{filepath.Join(pipeDir, "control")}
	// Mirror the real daemon's layout for each configured server:
	// <pipeDir>/<server>/<namespace>/control. Only the "default" namespace is
	// materialized, matching what mpsd renders and what fractiond mounts.
	for _, server := range configuredServers(configPath(os.Args[1:])) {
		sockets = append(sockets, filepath.Join(pipeDir, server, "default", "control"))
	}

	for _, sock := range sockets {
		cleanup, err := listen(sock)
		if err != nil {
			log.Fatalf("fake-mps: %v", err)
		}
		defer cleanup()
		log.Printf("fake-mps: control socket ready at %s", sock)
	}

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

// listen creates path's parent directories and binds a UNIX socket there,
// accepting and immediately closing connections so the path stays a live
// listening socket without ever blocking a would-be client. The returned
// func closes the listener and removes the socket.
func listen(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(path) // clear any stale socket before binding

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	return func() {
		_ = ln.Close()
		_ = os.Remove(path)
	}, nil
}

// configPath returns the value of the -a flag (the MPS config file) the mpsd
// supervisor passes, or "" if absent. Hand-parsed rather than via the flag
// package because the real binary's flags are single-dash with a mix of forms
// (-p 3 -m -f -a <path>) that flag.Parse would reject.
func configPath(args []string) string {
	for i, arg := range args {
		if arg == "-a" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// configuredServers returns the names of the [servers.<name>] sections in the
// MPS config. Reading them (instead of hardcoding "shared") keeps the fake
// honest about the sm-sharing chicken bit: with it disabled mpsd emits no
// [servers.shared] section, so no shared socket appears here either, exactly
// as on a real node.
func configuredServers(path string) []string {
	if path == "" {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		// Not fatal: the supervisor's readiness only needs the top-level
		// control socket, and failing here would mask the real error.
		log.Printf("fake-mps: reading config %s: %v (no server sockets created)", path, err)
		return nil
	}

	var servers []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		name, ok := strings.CutPrefix(line, "[servers.")
		if !ok {
			continue
		}
		name, ok = strings.CutSuffix(name, "]")
		if !ok {
			continue
		}
		if name = strings.Trim(name, `"`); name != "" {
			servers = append(servers, name)
		}
	}
	return servers
}
