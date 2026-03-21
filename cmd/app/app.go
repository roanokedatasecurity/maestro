package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/roanokedatasecurity/maestro/internal/api"
	"github.com/roanokedatasecurity/maestro/internal/bus"
	"github.com/roanokedatasecurity/maestro/internal/job"
	"github.com/roanokedatasecurity/maestro/internal/player"
	"github.com/roanokedatasecurity/maestro/internal/store"
)

const version = "0.2.0"

// App holds application state and lifecycle hooks for the Wails runtime.
type App struct {
	ctx        context.Context
	db         *store.Store
	apiServer  *api.Server
	socketPath string
}

// NewApp creates a new App instance.
func NewApp() *App {
	return &App{}
}

// OnStartup is called once when the app starts. It opens the database, applies
// migrations, and starts the Unix socket HTTP server in-process. The server is
// always-on — there is no config toggle.
func (a *App) OnStartup(ctx context.Context) {
	a.ctx = ctx

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "maestro/app: home dir: %v\n", err)
		return
	}

	dbPath := filepath.Join(home, ".maestro", "maestro.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "maestro/app: mkdir %s: %v\n", filepath.Dir(dbPath), err)
		return
	}

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maestro/app: store.Open: %v\n", err)
		return
	}
	a.db = st

	ps := player.New(st)
	js := job.New(st)
	bs := bus.New(st, ps, js)

	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("maestro-app-%d.sock", os.Getpid()))
	a.socketPath = sockPath

	srv, err := api.New(sockPath, bs, ps, js, st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "maestro/app: api.New: %v\n", err)
		st.Close()
		a.db = nil
		return
	}
	a.apiServer = srv

	// Intentional global env mutation: child processes (Conductor PTY, players)
	// inherit this variable at spawn time. Single-process Wails app guarantees
	// only one instance sets it.
	os.Setenv("MAESTRO_SOCKET", sockPath)

	go func() {
		if err := srv.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "maestro/app: server error: %v\n", err)
		}
	}()
}

// OnShutdown is called when the app is about to quit. It shuts down the HTTP
// server, removes the socket, and closes the database — mirroring the graceful
// shutdown in cmd/maestro/main.go.
func (a *App) OnShutdown(ctx context.Context) {
	if a.apiServer != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.apiServer.Stop(shutCtx); err != nil {
			fmt.Fprintf(os.Stderr, "maestro/app: shutdown error: %v\n", err)
		}
	}
	if a.socketPath != "" {
		os.Remove(a.socketPath)
	}
	if a.db != nil {
		if err := a.db.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "maestro/app: db close: %v\n", err)
		}
	}
}

// OnDomReady is called after the frontend DOM has fully loaded.
func (a *App) OnDomReady(ctx context.Context) {}

// GetStatus returns a JSON string proving the Go→React round-trip works.
// Fields: db_ok (bool), socket_ok (bool), version (string).
func (a *App) GetStatus() string {
	status := map[string]any{
		"db_ok":     a.db != nil,
		"socket_ok": a.apiServer != nil,
		"version":   version,
	}
	b, err := json.Marshal(status)
	if err != nil {
		// String concatenation intentional: avoids a recursive dependency on
		// json.Marshal in an error path where Marshal itself may be broken.
		return `{"db_ok":false,"socket_ok":false,"version":"` + version + `","error":"marshal failed"}`
	}
	return string(b)
}
