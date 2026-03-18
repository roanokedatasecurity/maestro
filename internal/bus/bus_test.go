package bus

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/roanokedatasecurity/maestro/internal/job"
	"github.com/roanokedatasecurity/maestro/internal/player"
	"github.com/roanokedatasecurity/maestro/internal/store"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type testEnv struct {
	svc       *Service
	store     *store.Store
	players   *player.Service
	jobs      *job.Service
	conductor *player.Player
	coder     *player.Player
}

// newEnv creates a store, services, and registers a Conductor + Coder player.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	s := openTestStore(t)
	ps := player.New(s)
	js := job.New(s)
	svc := New(s, ps, js)

	conductor, err := ps.Register("conductor", true)
	if err != nil {
		t.Fatalf("register conductor: %v", err)
	}
	coder, err := ps.Register("coder", false)
	if err != nil {
		t.Fatalf("register coder: %v", err)
	}
	return &testEnv{
		svc:       svc,
		store:     s,
		players:   ps,
		jobs:      js,
		conductor: conductor,
		coder:     coder,
	}
}

// sendAssignment is a shorthand for sending a Normal Assignment from Conductor to coder.
func (e *testEnv) sendAssignment(t *testing.T, payload string) {
	t.Helper()
	if err := e.svc.Send(e.conductor.ID, e.coder.ID, store.MessageTypeAssignment, store.PriorityNormal, payload, false); err != nil {
		t.Fatalf("Send Assignment: %v", err)
	}
}

// makeCoderRunning puts the coder into Running state by transitioning directly
// (bypasses Send so no message is created — useful for pre-conditioning tests).
func (e *testEnv) makeCoderRunning(t *testing.T) {
	t.Helper()
	if err := e.players.Transition(e.coder.ID, player.StatusRunning); err != nil {
		t.Fatalf("transition coder to Running: %v", err)
	}
}

// makeCoderIdle puts the coder back to Idle (Running → Idle is legal in the state machine).
func (e *testEnv) makeCoderIdle(t *testing.T) {
	t.Helper()
	if err := e.players.Transition(e.coder.ID, player.StatusIdle); err != nil {
		t.Fatalf("transition coder to Idle: %v", err)
	}
}

// activeJobForCoder returns the first InProgress or Backgrounded job for coder,
// failing the test if none exists.
func (e *testEnv) activeJobForCoder(t *testing.T) *store.Job {
	t.Helper()
	jobs, err := e.store.ListActiveJobs(e.coder.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("expected at least one active job for coder, got none")
	}
	return jobs[0]
}

// ─── TestRoutingEnforcement ────────────────────────────────────────────────────

// TestRoutingEnforcement verifies that illegal message paths are rejected with
// descriptive errors and that no message is persisted on rejection.
func TestRoutingEnforcement(t *testing.T) {
	e := newEnv(t)

	t.Run("player to player rejected", func(t *testing.T) {
		// Register a second player so we have two non-conductors.
		researcher, err := e.players.Register("researcher", false)
		if err != nil {
			t.Fatalf("register researcher: %v", err)
		}
		err = e.svc.Send(
			e.coder.ID, researcher.ID,
			store.MessageTypeAssignment, store.PriorityNormal,
			"do some research", false,
		)
		if err == nil {
			t.Fatal("expected error for Player→Player, got nil")
		}
		if !strings.Contains(err.Error(), "Player-to-Player") {
			t.Errorf("error should mention Player-to-Player, got: %v", err)
		}
	})

	t.Run("player assignment to conductor rejected", func(t *testing.T) {
		err := e.svc.Send(
			e.coder.ID, e.conductor.ID,
			store.MessageTypeAssignment, store.PriorityNormal,
			"you do it", false,
		)
		if err == nil {
			t.Fatal("expected error for Player→Conductor Assignment, got nil")
		}
		if !strings.Contains(err.Error(), "Assignment") {
			t.Errorf("error should mention Assignment, got: %v", err)
		}
	})

	t.Run("conductor assignment to conductor rejected", func(t *testing.T) {
		err := e.svc.Send(
			e.conductor.ID, e.conductor.ID,
			store.MessageTypeAssignment, store.PriorityNormal,
			"assign to self", false,
		)
		if err == nil {
			t.Fatal("expected error for Conductor→Conductor, got nil")
		}
	})

	t.Run("player done to conductor allowed", func(t *testing.T) {
		// Done signal from player to conductor should pass routing (even though
		// in practice the API layer calls HandleDone instead of Send for signals).
		err := e.svc.Send(
			e.coder.ID, e.conductor.ID,
			store.MessageTypeDone, store.PriorityNormal,
			"finished", false,
		)
		// This will pass routing but the conductor is not Idle so Deliver is a no-op.
		if err != nil {
			t.Errorf("expected Done Player→Conductor to be allowed, got: %v", err)
		}
	})
}

// ─── TestPriorityOrdering ─────────────────────────────────────────────────────

// TestPriorityOrdering verifies that a High-priority Assignment is delivered
// before a Normal-priority Assignment regardless of arrival order.
func TestPriorityOrdering(t *testing.T) {
	e := newEnv(t)

	// Pre-condition: put coder in Running so both sends are queued.
	e.makeCoderRunning(t)

	if err := e.svc.Send(
		e.conductor.ID, e.coder.ID,
		store.MessageTypeAssignment, store.PriorityNormal,
		"normal task", false,
	); err != nil {
		t.Fatalf("Send Normal: %v", err)
	}
	if err := e.svc.Send(
		e.conductor.ID, e.coder.ID,
		store.MessageTypeAssignment, store.PriorityHigh,
		"urgent task", false,
	); err != nil {
		t.Fatalf("Send High: %v", err)
	}

	// Both messages are undelivered (player was Running).
	msgs, err := e.store.ListUndelivered(e.coder.ID)
	if err != nil {
		t.Fatalf("ListUndelivered: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 undelivered messages, got %d", len(msgs))
	}

	// Transition to Idle and deliver.
	e.makeCoderIdle(t)
	if err := e.svc.Deliver(e.coder.ID); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	// After delivery, one message should remain: the Normal one.
	remaining, err := e.store.ListUndelivered(e.coder.ID)
	if err != nil {
		t.Fatalf("ListUndelivered after deliver: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining message, got %d", len(remaining))
	}
	if remaining[0].Priority != store.PriorityNormal {
		t.Errorf("expected remaining message to be Normal priority, got %q", remaining[0].Priority)
	}

	// The delivered (High) message should have delivered_at set.
	jobs, err := e.store.ListActiveJobs(e.coder.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 active job (from High assignment), got %d", len(jobs))
	}
	if !strings.Contains(jobs[0].Payload, "urgent task") {
		t.Errorf("expected job payload to contain urgent task, got: %q", jobs[0].Payload)
	}
}

// ─── TestAssignmentCreatesJob ─────────────────────────────────────────────────

// TestAssignmentCreatesJob verifies that delivering an Assignment creates a Job
// with a non-empty scratchpad path.
func TestAssignmentCreatesJob(t *testing.T) {
	e := newEnv(t)
	// Coder starts Idle; Send triggers immediate Deliver.
	e.sendAssignment(t, "implement the feature")

	jobs, err := e.store.ListActiveJobs(e.coder.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 active job, got %d", len(jobs))
	}
	j := jobs[0]
	if j.ScratchpadPath == "" {
		t.Error("expected non-empty ScratchpadPath")
	}
	if j.Status != store.JobStatusInProgress {
		t.Errorf("expected job status InProgress, got %q", j.Status)
	}
	if j.MessageID == "" {
		t.Error("expected non-empty MessageID")
	}
}

// ─── TestPayloadInjection ─────────────────────────────────────────────────────

// TestPayloadInjection verifies that the delivered Job payload contains both
// $MAESTRO_JOB_ID and $MAESTRO_SCRATCHPAD appended to the original assignment text.
func TestPayloadInjection(t *testing.T) {
	e := newEnv(t)
	e.sendAssignment(t, "write the tests")

	j := e.activeJobForCoder(t)

	if !strings.Contains(j.Payload, "$MAESTRO_JOB_ID="+j.ID) {
		t.Errorf("payload missing $MAESTRO_JOB_ID; payload:\n%s", j.Payload)
	}
	if !strings.Contains(j.Payload, "$MAESTRO_SCRATCHPAD="+j.ScratchpadPath) {
		t.Errorf("payload missing $MAESTRO_SCRATCHPAD; payload:\n%s", j.Payload)
	}
	if !strings.Contains(j.Payload, "write the tests") {
		t.Errorf("payload missing original assignment text; payload:\n%s", j.Payload)
	}
}

// ─── TestQueuedWhileRunning ───────────────────────────────────────────────────

// TestQueuedWhileRunning verifies that an Assignment sent to a Running player is
// queued and not delivered immediately.
func TestQueuedWhileRunning(t *testing.T) {
	e := newEnv(t)
	e.makeCoderRunning(t)

	e.sendAssignment(t, "do this while running")

	// No job should exist — message is queued, not delivered.
	jobs, err := e.store.ListActiveJobs(e.coder.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("expected 0 active jobs (queued, not delivered), got %d", len(jobs))
	}

	// Message should be undelivered.
	msgs, err := e.store.ListUndelivered(e.coder.ID)
	if err != nil {
		t.Fatalf("ListUndelivered: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 queued message, got %d", len(msgs))
	}
}

// ─── TestHandleDone ───────────────────────────────────────────────────────────

// TestHandleDone verifies that HandleDone transitions Job→Complete, Player→Idle,
// and enqueues a Done notification to the Conductor.
func TestHandleDone(t *testing.T) {
	e := newEnv(t)
	// Deliver an assignment to get a Job.
	e.sendAssignment(t, "build something")
	j := e.activeJobForCoder(t)

	if err := e.svc.HandleDone(e.coder.ID, j.ID, "all done!"); err != nil {
		t.Fatalf("HandleDone: %v", err)
	}

	// Job should be Complete.
	updated, err := e.store.GetJob(j.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if updated.Status != store.JobStatusComplete {
		t.Errorf("expected job Complete, got %q", updated.Status)
	}
	if updated.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}

	// Player should be Idle.
	p, err := e.players.Get(e.coder.ID)
	if err != nil {
		t.Fatalf("Get player: %v", err)
	}
	if p.Status != player.StatusIdle {
		t.Errorf("expected player Idle, got %q", p.Status)
	}

	// Conductor should have a Done notification.
	notifs, err := e.svc.GetNotifications(0, 0)
	if err != nil {
		t.Fatalf("GetNotifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	n := notifs[0]
	if n.Type != "Done" {
		t.Errorf("expected notification type Done, got %q", n.Type)
	}
	if n.JobID == nil || *n.JobID != j.ID {
		t.Errorf("expected notification JobID %q, got %v", j.ID, n.JobID)
	}
}

// ─── TestHandleDone_EmptyJobID ────────────────────────────────────────────────

func TestHandleDone_EmptyJobID(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.HandleDone(e.coder.ID, "", "summary"); err == nil {
		t.Fatal("expected error for empty jobID, got nil")
	}
}

// ─── TestHandleBackground ────────────────────────────────────────────────────

// TestHandleBackground verifies that signaling Background transitions the Job to
// Backgrounded, transitions the player to Idle, and triggers delivery of the
// next queued Assignment.
func TestHandleBackground(t *testing.T) {
	e := newEnv(t)

	// Deliver first assignment.
	e.sendAssignment(t, "first task")
	j1 := e.activeJobForCoder(t)

	// Queue a second assignment while coder is Running.
	if err := e.svc.Send(
		e.conductor.ID, e.coder.ID,
		store.MessageTypeAssignment, store.PriorityNormal,
		"second task", false,
	); err != nil {
		t.Fatalf("Send second assignment: %v", err)
	}

	// Signal Background on first job.
	if err := e.svc.HandleBackground(e.coder.ID, j1.ID); err != nil {
		t.Fatalf("HandleBackground: %v", err)
	}

	// First job should be Backgrounded.
	updated, err := e.store.GetJob(j1.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if updated.Status != store.JobStatusBackgrounded {
		t.Errorf("expected job Backgrounded, got %q", updated.Status)
	}

	// The second assignment should now be delivered (player was Idle after Background).
	// Player should be Running again (second assignment delivered).
	p, err := e.players.Get(e.coder.ID)
	if err != nil {
		t.Fatalf("Get player: %v", err)
	}
	if p.Status != player.StatusRunning {
		t.Errorf("expected player Running after next delivery, got %q", p.Status)
	}

	// There should be 2 active jobs: first (Backgrounded) + second (InProgress).
	activeJobs, err := e.store.ListActiveJobs(e.coder.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}
	if len(activeJobs) != 2 {
		t.Errorf("expected 2 active jobs, got %d", len(activeJobs))
	}
}

// ─── TestHandlePlayerDead ────────────────────────────────────────────────────

// TestHandlePlayerDead verifies that when a player dies, all active Jobs are
// moved to DeadLetter and a Lifecycle notification is enqueued for each.
func TestHandlePlayerDead(t *testing.T) {
	e := newEnv(t)

	// Deliver two assignments sequentially so we have two active jobs.
	// First assignment delivered immediately (coder is Idle).
	e.sendAssignment(t, "first task")
	j1 := e.activeJobForCoder(t)

	// Background first job to get back to Idle, then deliver second.
	if err := e.svc.HandleBackground(e.coder.ID, j1.ID); err != nil {
		t.Fatalf("HandleBackground j1: %v", err)
	}
	// Need to explicitly transition j1 back to InProgress to simulate both active
	// — actually Backgrounded counts as active (ListActiveJobs includes it).
	// Deliver second assignment.
	e.sendAssignment(t, "second task")

	// Verify both are active.
	activeJobs, err := e.store.ListActiveJobs(e.coder.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs before dead: %v", err)
	}
	if len(activeJobs) != 2 {
		t.Fatalf("expected 2 active jobs before death, got %d", len(activeJobs))
	}

	// Kill the player.
	if err := e.svc.HandlePlayerDead(e.coder.ID); err != nil {
		t.Fatalf("HandlePlayerDead: %v", err)
	}

	// Player should be Dead.
	p, err := e.players.Get(e.coder.ID)
	if err != nil {
		t.Fatalf("Get player: %v", err)
	}
	if p.Status != player.StatusDead {
		t.Errorf("expected player Dead, got %q", p.Status)
	}

	// All active jobs should now be DeadLetter.
	for _, aj := range activeJobs {
		updated, err := e.store.GetJob(aj.ID)
		if err != nil {
			t.Fatalf("GetJob %q: %v", aj.ID, err)
		}
		if updated.Status != store.JobStatusDeadLetter {
			t.Errorf("job %q: expected DeadLetter, got %q", aj.ID, updated.Status)
		}
	}

	// Two Lifecycle notifications should be enqueued (one per dead-letter job).
	notifs, err := e.svc.GetNotifications(0, 0)
	if err != nil {
		t.Fatalf("GetNotifications: %v", err)
	}
	if len(notifs) != 2 {
		t.Fatalf("expected 2 Lifecycle notifications, got %d", len(notifs))
	}
	for _, n := range notifs {
		if n.Type != "Lifecycle" {
			t.Errorf("expected notification type Lifecycle, got %q", n.Type)
		}
	}
}

// ─── TestMarkNotificationRead ────────────────────────────────────────────────

func TestMarkNotificationRead(t *testing.T) {
	e := newEnv(t)
	e.sendAssignment(t, "task")
	j := e.activeJobForCoder(t)

	if err := e.svc.HandleDone(e.coder.ID, j.ID, "done"); err != nil {
		t.Fatalf("HandleDone: %v", err)
	}

	notifs, err := e.svc.GetNotifications(0, 0)
	if err != nil {
		t.Fatalf("GetNotifications: %v", err)
	}
	if len(notifs) == 0 {
		t.Fatal("expected notifications, got none")
	}

	nid := notifs[0].ID
	if err := e.svc.MarkNotificationRead(nid); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}

	// Should no longer appear in unread list.
	after, err := e.svc.GetNotifications(0, 0)
	if err != nil {
		t.Fatalf("GetNotifications after mark: %v", err)
	}
	for _, n := range after {
		if n.ID == nid {
			t.Errorf("notification %q still in unread list after MarkNotificationRead", nid)
		}
	}
}

// ─── TestGetNotificationsLimit ────────────────────────────────────────────────

func TestGetNotificationsLimit(t *testing.T) {
	e := newEnv(t)

	// Create 3 notifications via HandleDone calls.
	for i := 0; i < 3; i++ {
		// For each, deliver an assignment (player must be Idle each time).
		e.sendAssignment(t, "task")
		j := e.activeJobForCoder(t)
		if err := e.svc.HandleDone(e.coder.ID, j.ID, "done"); err != nil {
			t.Fatalf("HandleDone iteration %d: %v", i, err)
		}
	}

	all, err := e.svc.GetNotifications(0, 0)
	if err != nil {
		t.Fatalf("GetNotifications(0): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 notifications, got %d", len(all))
	}

	limited, err := e.svc.GetNotifications(2, 0)
	if err != nil {
		t.Fatalf("GetNotifications(2): %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("expected 2 notifications with limit=2, got %d", len(limited))
	}
}
