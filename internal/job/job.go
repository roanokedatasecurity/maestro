// Package job implements the Job domain model and lifecycle state machine.
// Persistence is delegated entirely to the store package — no raw SQL here.
// Maestro owns scratchpad path assignment; players receive the path via
// $MAESTRO_SCRATCHPAD injected into the Assignment.
package job

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/roanokedatasecurity/maestro/internal/store"
)

// scratchpadBase returns the persistent scratchpad directory (~/.maestro/scratch).
// Scratchpads must survive Maestro restarts — /tmp is cleared on OS reboot and
// would destroy dead-letter recovery artifacts still referenced by Job records.
func scratchpadBase() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".maestro", "scratch"), nil
}

// legalTransitions defines every allowed JobStatus → JobStatus move.
// Complete and DeadLetter are terminal — no outbound transitions.
var legalTransitions = map[store.JobStatus]map[store.JobStatus]bool{
	store.JobStatusInProgress: {
		store.JobStatusComplete:     true,
		store.JobStatusBackgrounded: true,
		store.JobStatusDeadLetter:   true,
	},
	store.JobStatusBackgrounded: {
		store.JobStatusInProgress: true,
		store.JobStatusComplete:   true,
		store.JobStatusDeadLetter: true,
	},
	// Complete and DeadLetter are terminal — no outbound transitions.
	store.JobStatusComplete:   {},
	store.JobStatusDeadLetter: {},
}

// Service wraps the store and enforces the Job lifecycle state machine.
type Service struct {
	store *store.Store
}

// New creates a Service backed by the given store.
func New(s *store.Store) *Service {
	return &Service{store: s}
}

// Create registers a new InProgress Job for a delivered Assignment.
// It assigns a scratchpad path (~/.maestro/scratch/<job-id>.md) and ensures
// the scratchpad directory exists before persisting via the store.
func (svc *Service) Create(messageID, playerID, playerName, payload string) (*store.Job, error) {
	dir, err := scratchpadBase()
	if err != nil {
		return nil, fmt.Errorf("job.Create: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("job.Create: ensure scratchpad dir: %w", err)
	}

	// Create the job first to get its store-generated ID, then set the path.
	j, err := svc.store.CreateJob(messageID, playerID, playerName, payload, "")
	if err != nil {
		return nil, fmt.Errorf("job.Create: %w", err)
	}

	scratchpad := filepath.Join(dir, j.ID+".md")
	if err := svc.store.SetJobScratchpad(j.ID, scratchpad); err != nil {
		return nil, fmt.Errorf("job.Create: set scratchpad: %w", err)
	}

	j.ScratchpadPath = scratchpad
	return j, nil
}

// Transition moves a Job to newStatus, enforcing the state machine.
// Sets CompletedAt when transitioning to Complete or DeadLetter.
// Invalid transitions return a descriptive error — no silent no-ops.
func (svc *Service) Transition(id string, newStatus store.JobStatus) error {
	j, err := svc.store.GetJob(id)
	if err != nil {
		return fmt.Errorf("job.Transition: get job: %w", err)
	}

	allowed, ok := legalTransitions[j.Status]
	if !ok || !allowed[newStatus] {
		return fmt.Errorf("job.Transition: illegal transition %s → %s for job %q", j.Status, newStatus, id)
	}

	switch newStatus {
	case store.JobStatusComplete:
		if err := svc.store.SetJobCompleted(id); err != nil {
			return fmt.Errorf("job.Transition: %w", err)
		}
	case store.JobStatusDeadLetter:
		if err := svc.store.SetJobDeadLetter(id); err != nil {
			return fmt.Errorf("job.Transition: %w", err)
		}
	default:
		if err := svc.store.UpdateJobStatus(id, newStatus); err != nil {
			return fmt.Errorf("job.Transition: %w", err)
		}
	}
	return nil
}

// Get returns a single Job by ID.
func (svc *Service) Get(id string) (*store.Job, error) {
	j, err := svc.store.GetJob(id)
	if err != nil {
		return nil, fmt.Errorf("job.Get: %w", err)
	}
	return j, nil
}

// List returns all Jobs ordered by creation time ascending.
func (svc *Service) List() ([]*store.Job, error) {
	jobs, err := svc.store.ListJobs()
	if err != nil {
		return nil, fmt.Errorf("job.List: %w", err)
	}
	return jobs, nil
}

// ListByPlayer returns all Jobs for the given playerID, ordered oldest first.
func (svc *Service) ListByPlayer(playerID string) ([]*store.Job, error) {
	jobs, err := svc.store.ListJobsByPlayer(playerID)
	if err != nil {
		return nil, fmt.Errorf("job.ListByPlayer: %w", err)
	}
	return jobs, nil
}

// ListByStatus returns all Jobs with the given status, ordered oldest first.
func (svc *Service) ListByStatus(status store.JobStatus) ([]*store.Job, error) {
	jobs, err := svc.store.ListJobsByStatus(status)
	if err != nil {
		return nil, fmt.Errorf("job.ListByStatus: %w", err)
	}
	return jobs, nil
}
