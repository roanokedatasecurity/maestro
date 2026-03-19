// Package api implements the Maestro Unix socket HTTP server — the IPC endpoint
// surface that players and the Conductor use to coordinate via the message bus.
//
// The server listens on a Unix domain socket at $MAESTRO_SOCKET. Every player
// inherits that env var at spawn time and uses it to call the bus endpoints.
//
// Endpoint surface:
//
//	POST   /players                         register a player
//	GET    /players                         list all players
//	GET    /players/{id}                    get a player by ID
//	POST   /players/{id}/message            send Assignment to player (Conductor only)
//	POST   /players/{id}/done               player signals Done
//	POST   /players/{id}/blocked            player signals Blocked (wait=true holds open)
//	POST   /players/{id}/background         player signals Background
//	GET    /players/{id}/queue              inspect undelivered messages for a player
//	GET    /jobs                            list all Jobs
//	GET    /jobs/{id}                       get a Job by ID
//	GET    /conductor/notifications         get unread Conductor notifications
//	POST   /conductor/notifications/{id}/read  mark a notification read
//	POST   /conductor/approvals/{id}/decide    record an approval decision
package api

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/roanokedatasecurity/maestro/internal/bus"
	"github.com/roanokedatasecurity/maestro/internal/job"
	"github.com/roanokedatasecurity/maestro/internal/player"
	"github.com/roanokedatasecurity/maestro/internal/store"
)

// Server is the Maestro IPC HTTP server. It owns a Unix socket listener and
// routes all IPC requests to the bus, player, and job services.
type Server struct {
	bus     *bus.Service
	players *player.Service
	jobs    *job.Service
	store   *store.Store
	srv     *http.Server
	ln      net.Listener
	handler http.Handler
}

// New opens a Unix socket at socketPath and returns a ready-to-serve Server.
func New(socketPath string, b *bus.Service, p *player.Service, j *job.Service, s *store.Store) (*Server, error) {
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("api.New: listen on %q: %w", socketPath, err)
	}
	return newServer(ln, b, p, j, s), nil
}

// newServer constructs a Server backed by the provided listener. Used by New
// and by tests (which supply a TCP listener instead of a Unix socket).
func newServer(ln net.Listener, b *bus.Service, p *player.Service, j *job.Service, s *store.Store) *Server {
	server := &Server{
		bus:     b,
		players: p,
		jobs:    j,
		store:   s,
		ln:      ln,
	}
	mux := http.NewServeMux()
	server.routes(mux)
	server.handler = mux
	server.srv = &http.Server{Handler: mux}
	return server
}

// Handler returns the HTTP handler for this server. Useful for testing with
// net/http/httptest without needing a real socket listener.
func (s *Server) Handler() http.Handler { return s.handler }

// Start begins serving on the listener. It blocks until the server is shut down.
// Returns nil when Stop is called (http.ErrServerClosed is swallowed).
func (s *Server) Start() error {
	if err := s.srv.Serve(s.ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("api.Start: %w", err)
	}
	return nil
}

// Stop gracefully shuts down the server using the provided context.
func (s *Server) Stop(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// routes registers all IPC endpoints on mux.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /players", s.handleRegisterPlayer)
	mux.HandleFunc("GET /players", s.handleListPlayers)
	mux.HandleFunc("GET /players/{id}", s.handleGetPlayer)
	mux.HandleFunc("POST /players/{id}/message", s.handleSendMessage)
	mux.HandleFunc("POST /players/{id}/done", s.handleDone)
	mux.HandleFunc("POST /players/{id}/blocked", s.handleBlocked)
	mux.HandleFunc("POST /players/{id}/background", s.handleBackground)
	mux.HandleFunc("GET /players/{id}/queue", s.handleGetQueue)
	mux.HandleFunc("GET /jobs", s.handleListJobs)
	mux.HandleFunc("GET /jobs/{id}", s.handleGetJob)
	mux.HandleFunc("GET /conductor/notifications", s.handleGetNotifications)
	mux.HandleFunc("POST /conductor/notifications/{id}/read", s.handleMarkNotificationRead)
	mux.HandleFunc("POST /conductor/approvals/{id}/decide", s.handleDecide)
}
