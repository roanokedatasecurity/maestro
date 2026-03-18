package job_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/roanokedatasecurity/maestro/internal/job"
	"github.com/roanokedatasecurity/maestro/internal/store"
)

// openTestStore opens an in-memory SQLite store for test isolation.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// newSvc creates a job.Service backed by a fresh in-memory store.
func newSvc(t *testing.T) *job.Service {
	t.Helper()
	return job.New(openTestStore(t))
}

func TestCreateJob(t *testing.T) {
	svc := newSvc(t)

	j, err := svc.Create("msg-1", "player-1", "Coder", "implement feature X")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Scratchpad path must be set and follow the convention.
	if j.ScratchpadPath == "" {
		t.Fatal("expected scratchpad path to be set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	dir := filepath.Join(home, ".maestro", "scratch")
	expected := filepath.Join(dir, j.ID+".md")
	if j.ScratchpadPath != expected {
		t.Errorf("scratchpad path = %q, want %q", j.ScratchpadPath, expected)
	}

	// Directory must exist.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("expected scratchpad dir %q to exist after Create", dir)
	}

	// Status must be InProgress.
	if j.Status != store.JobStatusInProgress {
		t.Errorf("status = %q, want InProgress", j.Status)
	}

	// CompletedAt must be nil on a fresh job.
	if j.CompletedAt != nil {
		t.Errorf("expected CompletedAt to be nil, got %v", j.CompletedAt)
	}
}

func TestValidTransitions(t *testing.T) {
	svc := newSvc(t)

	j, err := svc.Create("msg-2", "player-1", "Coder", "research topic Y")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// InProgress → Backgrounded
	if err := svc.Transition(j.ID, store.JobStatusBackgrounded); err != nil {
		t.Fatalf("InProgress → Backgrounded: %v", err)
	}

	// Backgrounded → InProgress (resume)
	if err := svc.Transition(j.ID, store.JobStatusInProgress); err != nil {
		t.Fatalf("Backgrounded → InProgress: %v", err)
	}

	// InProgress → Complete
	if err := svc.Transition(j.ID, store.JobStatusComplete); err != nil {
		t.Fatalf("InProgress → Complete: %v", err)
	}

	got, err := svc.Get(j.ID)
	if err != nil {
		t.Fatalf("Get after complete: %v", err)
	}
	if got.Status != store.JobStatusComplete {
		t.Errorf("final status = %q, want Complete", got.Status)
	}
}

func TestInvalidTransitions(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(svc *job.Service) string // returns job ID in starting state
		target store.JobStatus
	}{
		{
			name: "InProgress→InProgress",
			setup: func(svc *job.Service) string {
				j, err := svc.Create("msg-a", "p1", "Coder", "task")
				if err != nil {
					panic(err)
				}
				return j.ID
			},
			target: store.JobStatusInProgress,
		},
		{
			name: "Complete→InProgress",
			setup: func(svc *job.Service) string {
				j, err := svc.Create("msg-b", "p1", "Coder", "task")
				if err != nil {
					panic(err)
				}
				if err := svc.Transition(j.ID, store.JobStatusComplete); err != nil {
					panic(err)
				}
				return j.ID
			},
			target: store.JobStatusInProgress,
		},
		{
			name: "Complete→Backgrounded",
			setup: func(svc *job.Service) string {
				j, err := svc.Create("msg-c", "p1", "Coder", "task")
				if err != nil {
					panic(err)
				}
				if err := svc.Transition(j.ID, store.JobStatusComplete); err != nil {
					panic(err)
				}
				return j.ID
			},
			target: store.JobStatusBackgrounded,
		},
		{
			name: "DeadLetter→Complete",
			setup: func(svc *job.Service) string {
				j, err := svc.Create("msg-d", "p1", "Coder", "task")
				if err != nil {
					panic(err)
				}
				if err := svc.Transition(j.ID, store.JobStatusDeadLetter); err != nil {
					panic(err)
				}
				return j.ID
			},
			target: store.JobStatusComplete,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newSvc(t)
			id := tc.setup(svc)
			err := svc.Transition(id, tc.target)
			if err == nil {
				t.Errorf("expected error for illegal transition, got nil")
			}
		})
	}
}

func TestDeadLetterSetsCompletedAt(t *testing.T) {
	svc := newSvc(t)

	j, err := svc.Create("msg-3", "player-1", "Coder", "task that will die")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Transition(j.ID, store.JobStatusDeadLetter); err != nil {
		t.Fatalf("Transition → DeadLetter: %v", err)
	}

	got, err := svc.Get(j.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("expected CompletedAt to be set after DeadLetter transition")
	}
	if got.Status != store.JobStatusDeadLetter {
		t.Errorf("status = %q, want DeadLetter", got.Status)
	}
}

func TestCompleteSetsCompletedAt(t *testing.T) {
	svc := newSvc(t)

	j, err := svc.Create("msg-4", "player-1", "Coder", "task that will complete")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Transition(j.ID, store.JobStatusComplete); err != nil {
		t.Fatalf("Transition → Complete: %v", err)
	}

	got, err := svc.Get(j.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CompletedAt == nil || got.CompletedAt.IsZero() {
		t.Error("expected CompletedAt to be set after Complete transition")
	}
	if got.Status != store.JobStatusComplete {
		t.Errorf("status = %q, want Complete", got.Status)
	}
}

func TestListByPlayer(t *testing.T) {
	svc := newSvc(t)

	// Create two jobs for player-A and one for player-B.
	j1, err := svc.Create("msg-5", "player-A", "Coder", "task one")
	if err != nil {
		t.Fatalf("Create j1: %v", err)
	}
	j2, err := svc.Create("msg-6", "player-A", "Coder", "task two")
	if err != nil {
		t.Fatalf("Create j2: %v", err)
	}
	_, err = svc.Create("msg-7", "player-B", "Researcher", "task three")
	if err != nil {
		t.Fatalf("Create j3: %v", err)
	}

	jobs, err := svc.ListByPlayer("player-A")
	if err != nil {
		t.Fatalf("ListByPlayer: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs for player-A, got %d", len(jobs))
	}

	ids := map[string]bool{jobs[0].ID: true, jobs[1].ID: true}
	if !ids[j1.ID] || !ids[j2.ID] {
		t.Errorf("got unexpected job IDs: %v %v", jobs[0].ID, jobs[1].ID)
	}
	for _, j := range jobs {
		if j.PlayerID != "player-A" {
			t.Errorf("expected player-A, got %q", j.PlayerID)
		}
	}
}

func TestListByStatus(t *testing.T) {
	svc := newSvc(t)

	// Two InProgress, one Backgrounded, one Complete.
	j1, err := svc.Create("msg-8", "player-1", "Coder", "task one")
	if err != nil {
		t.Fatalf("Create j1: %v", err)
	}
	j2, err := svc.Create("msg-9", "player-1", "Coder", "task two")
	if err != nil {
		t.Fatalf("Create j2: %v", err)
	}
	j3, err := svc.Create("msg-10", "player-2", "Coder", "task three")
	if err != nil {
		t.Fatalf("Create j3: %v", err)
	}
	j4, err := svc.Create("msg-11", "player-2", "Coder", "task four")
	if err != nil {
		t.Fatalf("Create j4: %v", err)
	}

	if err := svc.Transition(j3.ID, store.JobStatusBackgrounded); err != nil {
		t.Fatalf("background j3: %v", err)
	}
	if err := svc.Transition(j4.ID, store.JobStatusComplete); err != nil {
		t.Fatalf("complete j4: %v", err)
	}

	inProgress, err := svc.ListByStatus(store.JobStatusInProgress)
	if err != nil {
		t.Fatalf("ListByStatus InProgress: %v", err)
	}
	if len(inProgress) != 2 {
		t.Errorf("expected 2 InProgress jobs, got %d", len(inProgress))
	}
	inProgressIDs := map[string]bool{inProgress[0].ID: true, inProgress[1].ID: true}
	if !inProgressIDs[j1.ID] || !inProgressIDs[j2.ID] {
		t.Errorf("wrong InProgress jobs returned")
	}

	backgrounded, err := svc.ListByStatus(store.JobStatusBackgrounded)
	if err != nil {
		t.Fatalf("ListByStatus Backgrounded: %v", err)
	}
	if len(backgrounded) != 1 || backgrounded[0].ID != j3.ID {
		t.Errorf("expected 1 Backgrounded job (j3), got %d", len(backgrounded))
	}

	complete, err := svc.ListByStatus(store.JobStatusComplete)
	if err != nil {
		t.Fatalf("ListByStatus Complete: %v", err)
	}
	if len(complete) != 1 || complete[0].ID != j4.ID {
		t.Errorf("expected 1 Complete job (j4), got %d", len(complete))
	}
}
