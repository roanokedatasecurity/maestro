package player_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roanokedatasecurity/maestro/internal/player"
	"github.com/roanokedatasecurity/maestro/internal/store"
)

// newTestService opens a temporary SQLite database and returns a Service
// backed by it. The caller is responsible for cleanup via the returned func.
func newTestService(t *testing.T) (*player.Service, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	svc := player.New(s)
	return svc, func() {
		s.Close()
		os.Remove(dbPath)
	}
}

// TestRegisterAndGet verifies a persistence round-trip: Register → Get → fields match.
func TestRegisterAndGet(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	p, err := svc.Register("alice", false)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if p.ID == "" {
		t.Error("expected non-empty ID")
	}
	if p.Name != "alice" {
		t.Errorf("Name: want %q got %q", "alice", p.Name)
	}
	if p.Status != player.StatusIdle {
		t.Errorf("Status: want %q got %q", player.StatusIdle, p.Status)
	}
	if p.IsConductor {
		t.Error("IsConductor: want false")
	}
	if p.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if p.LastSeenAt.IsZero() {
		t.Error("LastSeenAt should not be zero")
	}

	// Round-trip via Get
	got, err := svc.Get(p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("Get ID mismatch: want %q got %q", p.ID, got.ID)
	}
	if got.Name != p.Name {
		t.Errorf("Get Name mismatch: want %q got %q", p.Name, got.Name)
	}
	if got.Status != player.StatusIdle {
		t.Errorf("Get Status: want Idle got %q", got.Status)
	}
}

// TestValidTransitions verifies every legal transition passes without error.
func TestValidTransitions(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	p, _ := svc.Register("worker", false)

	// Idle → Running
	if err := svc.Transition(p.ID, player.StatusRunning); err != nil {
		t.Fatalf("Idle→Running: %v", err)
	}

	// Running → Idle
	if err := svc.Transition(p.ID, player.StatusIdle); err != nil {
		t.Fatalf("Running→Idle: %v", err)
	}

	// Idle → Running again, then Running → Dead
	if err := svc.Transition(p.ID, player.StatusRunning); err != nil {
		t.Fatalf("Idle→Running (2nd): %v", err)
	}
	if err := svc.Transition(p.ID, player.StatusDead); err != nil {
		t.Fatalf("Running→Dead: %v", err)
	}

	got, _ := svc.Get(p.ID)
	if got.Status != player.StatusDead {
		t.Errorf("final status: want Dead got %q", got.Status)
	}
}

// TestInvalidTransitions verifies that illegal transitions return an error.
func TestInvalidTransitions(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(svc *player.Service) string // returns player ID
		target   player.Status
	}{
		{
			name: "Idle→Dead",
			setup: func(svc *player.Service) string {
				p, _ := svc.Register("p1", false)
				return p.ID
			},
			target: player.StatusDead,
		},
		{
			name: "Idle→Idle",
			setup: func(svc *player.Service) string {
				p, _ := svc.Register("p2", false)
				return p.ID
			},
			target: player.StatusIdle,
		},
		{
			name: "Dead→Running",
			setup: func(svc *player.Service) string {
				p, _ := svc.Register("p3", false)
				_ = svc.Transition(p.ID, player.StatusRunning)
				_ = svc.Transition(p.ID, player.StatusDead)
				return p.ID
			},
			target: player.StatusRunning,
		},
		{
			name: "Dead→Idle",
			setup: func(svc *player.Service) string {
				p, _ := svc.Register("p4", false)
				_ = svc.Transition(p.ID, player.StatusRunning)
				_ = svc.Transition(p.ID, player.StatusDead)
				return p.ID
			},
			target: player.StatusIdle,
		},
		{
			name: "Running→Running",
			setup: func(svc *player.Service) string {
				p, _ := svc.Register("p5", false)
				_ = svc.Transition(p.ID, player.StatusRunning)
				return p.ID
			},
			target: player.StatusRunning,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, cleanup := newTestService(t)
			defer cleanup()
			id := tc.setup(svc)
			err := svc.Transition(id, tc.target)
			if err == nil {
				t.Errorf("%s: expected error, got nil", tc.name)
			}
		})
	}
}

// TestConductorUniqueness verifies that registering a second live Conductor fails.
func TestConductorUniqueness(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	// First conductor — should succeed.
	_, err := svc.Register("conductor-1", true)
	if err != nil {
		t.Fatalf("first conductor Register: %v", err)
	}

	// Second conductor — must fail.
	_, err = svc.Register("conductor-2", true)
	if err == nil {
		t.Fatal("expected error registering second conductor, got nil")
	}
}

// TestConductorUniquenessDeadAllows verifies a new Conductor can register once
// the previous one is Dead (the uniqueness check excludes Dead players).
func TestConductorUniquenessDeadAllows(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	c1, _ := svc.Register("conductor-1", true)
	_ = svc.Transition(c1.ID, player.StatusRunning)
	if err := svc.MarkDead(c1.ID); err != nil {
		t.Fatalf("MarkDead conductor-1: %v", err)
	}

	// A new conductor should be allowed now.
	_, err := svc.Register("conductor-2", true)
	if err != nil {
		t.Fatalf("conductor-2 Register after conductor-1 Dead: %v", err)
	}
}

// TestMarkDead verifies MarkDead sets Dead status and is idempotent.
func TestMarkDead(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	p, _ := svc.Register("victim", false)
	_ = svc.Transition(p.ID, player.StatusRunning)

	if err := svc.MarkDead(p.ID); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}
	got, _ := svc.Get(p.ID)
	if got.Status != player.StatusDead {
		t.Errorf("after MarkDead: want Dead got %q", got.Status)
	}

	// Idempotent — second call must not error.
	if err := svc.MarkDead(p.ID); err != nil {
		t.Fatalf("MarkDead (idempotent): %v", err)
	}
}

// TestSetLastSeen verifies the heartbeat updates LastSeenAt.
func TestSetLastSeen(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	p, _ := svc.Register("heartbeat-player", false)
	before := p.LastSeenAt

	// SQLite datetime('now') has 1-second resolution — sleep past it.
	time.Sleep(1100 * time.Millisecond)

	if err := svc.SetLastSeen(p.ID); err != nil {
		t.Fatalf("SetLastSeen: %v", err)
	}
	got, _ := svc.Get(p.ID)
	if !got.LastSeenAt.After(before) {
		t.Errorf("LastSeenAt not updated: before=%v after=%v", before, got.LastSeenAt)
	}
}

// TestList verifies List returns all registered players.
func TestList(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()

	svc.Register("alpha", false)
	svc.Register("beta", false)
	svc.Register("gamma", true)

	players, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(players) != 3 {
		t.Errorf("List: want 3 players got %d", len(players))
	}
}
