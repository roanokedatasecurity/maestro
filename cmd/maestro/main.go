// Command maestro is the Maestro process: it wires all layers and starts the
// IPC server on a Unix domain socket so players can register and coordinate.
//
// Flags:
//
//	-db     path to the SQLite database (default ~/.maestro/maestro.db)
//	-socket path to the Unix socket     (default /tmp/maestro-<pid>.sock)
//
// After the socket is ready, the path is:
//  1. Exported as $MAESTRO_SOCKET so spawned players can discover it.
//  2. Written to /tmp/maestro.sock.path for shells that need it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/roanokedatasecurity/maestro/internal/api"
	"github.com/roanokedatasecurity/maestro/internal/bus"
	"github.com/roanokedatasecurity/maestro/internal/job"
	"github.com/roanokedatasecurity/maestro/internal/player"
	"github.com/roanokedatasecurity/maestro/internal/store"
)

func main() {
	defaultDB := filepath.Join(homeDir(), ".maestro", "maestro.db")
	defaultSock := fmt.Sprintf("/tmp/maestro-%d.sock", os.Getpid())

	dbPath := flag.String("db", defaultDB, "path to SQLite database")
	sockPath := flag.String("socket", defaultSock, "path to Unix socket")
	flag.Parse()

	// Ensure the DB directory exists before opening the database.
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "maestro: mkdir %s: %v\n", filepath.Dir(*dbPath), err)
		os.Exit(1)
	}

	// ── Layer 1: persistent store ─────────────────────────────────────────────
	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maestro: store.Open: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	// ── Layers 2–4: domain services ───────────────────────────────────────────
	ps := player.New(st)
	js := job.New(st)
	bs := bus.New(st, ps, js)

	// ── Layer 5: IPC server ───────────────────────────────────────────────────
	srv, err := api.New(*sockPath, bs, ps, js, st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maestro: api.New: %v\n", err)
		os.Exit(1)
	}

	// Export socket path so players spawned after this point can find the bus.
	os.Setenv("MAESTRO_SOCKET", *sockPath)
	if err := os.WriteFile("/tmp/maestro.sock.path", []byte(*sockPath), 0644); err != nil {
		// Non-fatal: the env var is the primary discovery mechanism.
		fmt.Fprintf(os.Stderr, "maestro: write socket path file: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "Maestro listening on %s\n", *sockPath)

	// Start serving in a goroutine so we can also wait on OS signals.
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "maestro: received %s, shutting down\n", sig)
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "maestro: server error: %v\n", err)
			os.Remove(*sockPath)
			os.Exit(1)
		}
		// Clean server shutdown (Stop was called from outside) — nothing to do.
		return
	}

	// Graceful shutdown: give in-flight requests up to 5 s to complete.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "maestro: shutdown error: %v\n", err)
	}
	os.Remove(*sockPath)
}

// homeDir returns the current user's home directory, falling back to $HOME.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}
