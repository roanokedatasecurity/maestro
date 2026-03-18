// Package player implements the Player domain model and status state machine.
// Persistence is delegated entirely to the store package — no raw SQL here.
package player

import (
	"fmt"
	"time"

	"github.com/roanokedatasecurity/maestro/internal/store"
)

// Status represents the lifecycle state of a Player.
type Status string

const (
	StatusIdle    Status = "Idle"
	StatusRunning Status = "Running"
	StatusDead    Status = "Dead"
)

// legalTransitions defines every allowed state → state move. Any transition
// not present here is illegal and will be rejected by Transition.
var legalTransitions = map[Status]map[Status]bool{
	StatusIdle: {
		StatusRunning: true,
	},
	StatusRunning: {
		StatusIdle: true,
		StatusDead: true,
	},
	// Dead is terminal — no outbound transitions.
	StatusDead: {},
}

// Player is the domain representation of a Maestro player (kid process).
type Player struct {
	ID          string
	Name        string
	Status      Status
	IsConductor bool
	CreatedAt   time.Time
	LastSeenAt  time.Time
}

// Service wraps the store and enforces the Player state machine.
type Service struct {
	store *store.Store
}

// New creates a Service backed by the given store.
func New(s *store.Store) *Service {
	return &Service{store: s}
}

// Register creates a new Player in Idle status.
// Returns an error if isConductor is true and a live Conductor already exists.
func (svc *Service) Register(name string, isConductor bool) (*Player, error) {
	p, err := svc.store.CreatePlayer(name, isConductor)
	if err != nil {
		return nil, fmt.Errorf("player.Register: %w", err)
	}
	return fromStore(p), nil
}

// Transition moves a Player to newStatus, enforcing the state machine.
// Invalid transitions return a descriptive error — no silent no-ops.
func (svc *Service) Transition(id string, newStatus Status) error {
	p, err := svc.store.GetPlayer(id)
	if err != nil {
		return fmt.Errorf("player.Transition: get player: %w", err)
	}
	current := Status(p.Status)
	if allowed, ok := legalTransitions[current]; !ok || !allowed[newStatus] {
		return fmt.Errorf("player.Transition: illegal transition %s → %s for player %q", current, newStatus, id)
	}
	if err := svc.store.UpdatePlayerStatus(id, store.PlayerStatus(newStatus)); err != nil {
		return fmt.Errorf("player.Transition: %w", err)
	}
	return nil
}

// SetLastSeen records a heartbeat for the given player.
func (svc *Service) SetLastSeen(id string) error {
	if err := svc.store.UpdateLastSeen(id); err != nil {
		return fmt.Errorf("player.SetLastSeen: %w", err)
	}
	return nil
}

// MarkDead transitions the player to Dead. Called when a PTY process exits.
// Idempotent — calling it on an already-Dead player is a no-op (no error).
func (svc *Service) MarkDead(id string) error {
	p, err := svc.store.GetPlayer(id)
	if err != nil {
		return fmt.Errorf("player.MarkDead: get player: %w", err)
	}
	if Status(p.Status) == StatusDead {
		return nil // already dead — idempotent
	}
	if err := svc.store.UpdatePlayerStatus(id, store.PlayerStatusDead); err != nil {
		return fmt.Errorf("player.MarkDead: %w", err)
	}
	return nil
}

// List returns all registered players ordered by creation time.
func (svc *Service) List() ([]*Player, error) {
	rows, err := svc.store.ListPlayers()
	if err != nil {
		return nil, fmt.Errorf("player.List: %w", err)
	}
	players := make([]*Player, len(rows))
	for i, r := range rows {
		players[i] = fromStore(r)
	}
	return players, nil
}

// Get returns a single player by ID.
func (svc *Service) Get(id string) (*Player, error) {
	p, err := svc.store.GetPlayer(id)
	if err != nil {
		return nil, fmt.Errorf("player.Get: %w", err)
	}
	return fromStore(p), nil
}

// fromStore converts a store.Player to the domain Player type.
func fromStore(p *store.Player) *Player {
	return &Player{
		ID:          p.ID,
		Name:        p.Name,
		Status:      Status(p.Status),
		IsConductor: p.IsConductor,
		CreatedAt:   p.CreatedAt,
		LastSeenAt:  p.LastSeenAt,
	}
}
