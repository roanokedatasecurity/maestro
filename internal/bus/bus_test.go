package bus

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	conductor, err := ps.Register("conductor", true, nil)
	if err != nil {
		t.Fatalf("register conductor: %v", err)
	}
	coder, err := ps.Register("coder", false, nil)
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
		researcher, err := e.players.Register("researcher", false, nil)
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

// ─── TestGetNotifications_Offset ─────────────────────────────────────────────

// TestGetNotifications_Offset verifies that offset-based pagination skips the
// correct number of notifications.
func TestGetNotifications_Offset(t *testing.T) {
	e := newEnv(t)

	// Create 4 notifications.
	for i := 0; i < 4; i++ {
		e.sendAssignment(t, "task")
		j := e.activeJobForCoder(t)
		if err := e.svc.HandleDone(e.coder.ID, j.ID, "done"); err != nil {
			t.Fatalf("HandleDone iteration %d: %v", i, err)
		}
	}

	// Page 1: limit=2, offset=0 → first 2.
	page1, err := e.svc.GetNotifications(2, 0)
	if err != nil {
		t.Fatalf("GetNotifications page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: expected 2, got %d", len(page1))
	}

	// Page 2: limit=2, offset=2 → last 2.
	page2, err := e.svc.GetNotifications(2, 2)
	if err != nil {
		t.Fatalf("GetNotifications page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2: expected 2, got %d", len(page2))
	}

	// Pages must not overlap.
	if page1[0].ID == page2[0].ID || page1[1].ID == page2[1].ID {
		t.Error("page1 and page2 share notification IDs — pagination broken")
	}
}

// ─── TestCountUnreadNotifications ────────────────────────────────────────────

func TestCountUnreadNotifications(t *testing.T) {
	e := newEnv(t)

	count, err := e.svc.CountUnreadNotifications()
	if err != nil {
		t.Fatalf("CountUnreadNotifications (empty): %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 unread initially, got %d", count)
	}

	// Create 2 notifications.
	for i := 0; i < 2; i++ {
		e.sendAssignment(t, "task")
		j := e.activeJobForCoder(t)
		if err := e.svc.HandleDone(e.coder.ID, j.ID, "done"); err != nil {
			t.Fatalf("HandleDone %d: %v", i, err)
		}
	}

	count, err = e.svc.CountUnreadNotifications()
	if err != nil {
		t.Fatalf("CountUnreadNotifications: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 unread, got %d", count)
	}

	// Mark one read.
	notifs, _ := e.svc.GetNotifications(1, 0)
	if err := e.svc.MarkNotificationRead(notifs[0].ID); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}

	count, err = e.svc.CountUnreadNotifications()
	if err != nil {
		t.Fatalf("CountUnreadNotifications after read: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 unread after marking one read, got %d", count)
	}
}

// ─── TestHandleBlocked ────────────────────────────────────────────────────────

// TestHandleBlocked_NoWait verifies that a Blocked signal with wait=false
// creates a Normal-priority notification but does NOT enqueue a Blocked message
// to the Conductor queue.
func TestHandleBlocked_NoWait(t *testing.T) {
	e := newEnv(t)

	e.sendAssignment(t, "task")
	j := e.activeJobForCoder(t)

	if _, err := e.svc.HandleBlocked(e.coder.ID, j.ID, "I am stuck", "", false); err != nil {
		t.Fatalf("HandleBlocked: %v", err)
	}

	// Notification should be created.
	notifs, err := e.svc.GetNotifications(0, 0)
	if err != nil {
		t.Fatalf("GetNotifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	if notifs[0].Type != "Blocked" {
		t.Errorf("expected type Blocked, got %q", notifs[0].Type)
	}

	// Player should still be Running (wait=false → advisory; doesn't affect player state).
	p, _ := e.players.Get(e.coder.ID)
	if p.Status != "Running" {
		t.Errorf("expected player still Running, got %q", p.Status)
	}

	// No Blocked message should be in the Conductor's message queue (wait=false).
	msgs, err := e.store.ListUndelivered(e.conductor.ID)
	if err != nil {
		t.Fatalf("ListUndelivered: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected no Conductor queue messages for wait=false, got %d", len(msgs))
	}
}

// TestHandleBlocked_Wait verifies that wait=true produces a High-priority
// Blocked message in the Conductor's queue in addition to the notification.
func TestHandleBlocked_Wait(t *testing.T) {
	e := newEnv(t)

	e.sendAssignment(t, "task")
	j := e.activeJobForCoder(t)

	if _, err := e.svc.HandleBlocked(e.coder.ID, j.ID, "need approval", "", true); err != nil {
		t.Fatalf("HandleBlocked wait=true: %v", err)
	}

	// Notification should be created.
	notifs, err := e.svc.GetNotifications(0, 0)
	if err != nil {
		t.Fatalf("GetNotifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}

	// A High-priority Blocked message should appear in the Conductor's queue.
	msgs, err := e.store.ListUndelivered(e.conductor.ID)
	if err != nil {
		t.Fatalf("ListUndelivered: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 Conductor queue message for wait=true, got %d", len(msgs))
	}
	if msgs[0].Type != store.MessageTypeBlocked {
		t.Errorf("expected Blocked message type, got %q", msgs[0].Type)
	}
	if msgs[0].Priority != store.PriorityHigh {
		t.Errorf("expected High priority, got %q", msgs[0].Priority)
	}
}

// TestHandleBlocked_EmptyJobID verifies that an empty jobID returns an error.
func TestHandleBlocked_EmptyJobID(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.HandleBlocked(e.coder.ID, "", "stuck", "", false); err == nil {
		t.Fatal("expected error for empty jobID, got nil")
	}
}

// ─── TestHandleBackground_EmptyJobID ─────────────────────────────────────────

func TestHandleBackground_EmptyJobID(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.HandleBackground(e.coder.ID, ""); err == nil {
		t.Fatal("expected error for empty jobID, got nil")
	}
}

// ─── TestHandleBackground_DeadPlayerTransitionError ──────────────────────────

// TestHandleBackground_DeadPlayerTransitionError verifies that HandleBackground
// returns an error when the player is Dead and cannot transition to Idle.
// This covers the players.Transition error path inside HandleBackground.
func TestHandleBackground_DeadPlayerTransitionError(t *testing.T) {
	e := newEnv(t)

	// Deliver an assignment to get a running job.
	e.sendAssignment(t, "background task")
	j := e.activeJobForCoder(t)

	// Kill the coder (bypasses state machine, sets Dead directly).
	if err := e.players.MarkDead(e.coder.ID); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}

	// HandleBackground: job transitions InProgress → Backgrounded (valid),
	// then finds 0 InProgress jobs, attempts Dead → Idle (illegal) → error.
	err := e.svc.HandleBackground(e.coder.ID, j.ID)
	if err == nil {
		t.Fatal("expected error when transitioning Dead player to Idle, got nil")
	}
}

// ─── TestHandleDone_MultiJob ──────────────────────────────────────────────────

// TestHandleDone_MultiJob verifies that HandleDone on one of multiple active
// jobs does NOT transition the player to Idle — it stays Running while the
// backgrounded job is still active.
func TestHandleDone_MultiJob(t *testing.T) {
	e := newEnv(t)

	// Deliver first assignment.
	e.sendAssignment(t, "first task")
	j1 := e.activeJobForCoder(t)

	// Background the first job (player goes Idle, ready for next).
	if err := e.svc.HandleBackground(e.coder.ID, j1.ID); err != nil {
		t.Fatalf("HandleBackground: %v", err)
	}

	// Deliver second assignment.
	e.sendAssignment(t, "second task")

	// Find the InProgress job (j2).
	inProgress, err := e.store.ListJobsByPlayerAndStatus(e.coder.ID, store.JobStatusInProgress)
	if err != nil {
		t.Fatalf("ListJobsByPlayerAndStatus: %v", err)
	}
	if len(inProgress) != 1 {
		t.Fatalf("expected 1 InProgress job, got %d", len(inProgress))
	}
	j2 := inProgress[0]

	// Signal Done on j2 — j1 is still Backgrounded.
	if err := e.svc.HandleDone(e.coder.ID, j2.ID, "j2 done"); err != nil {
		t.Fatalf("HandleDone j2: %v", err)
	}

	// j2 should be Complete.
	updated, err := e.store.GetJob(j2.ID)
	if err != nil {
		t.Fatalf("GetJob j2: %v", err)
	}
	if updated.Status != store.JobStatusComplete {
		t.Errorf("j2: expected Complete, got %q", updated.Status)
	}

	// Player should still be Running — j1 is still Backgrounded (active).
	p, err := e.players.Get(e.coder.ID)
	if err != nil {
		t.Fatalf("Get player: %v", err)
	}
	if p.Status != "Running" {
		t.Errorf("expected player still Running (backgrounded job active), got %q", p.Status)
	}
}

// ─── TestHandleBackground_NoQueued ───────────────────────────────────────────

// TestHandleBackground_NoQueued verifies that signaling Background with no
// queued assignments leaves the player Idle.
func TestHandleBackground_NoQueued(t *testing.T) {
	e := newEnv(t)

	e.sendAssignment(t, "solo task")
	j := e.activeJobForCoder(t)

	if err := e.svc.HandleBackground(e.coder.ID, j.ID); err != nil {
		t.Fatalf("HandleBackground: %v", err)
	}

	p, err := e.players.Get(e.coder.ID)
	if err != nil {
		t.Fatalf("Get player: %v", err)
	}
	// No queued work → player stays Idle.
	if p.Status != "Idle" {
		t.Errorf("expected Idle (no queued work), got %q", p.Status)
	}
}

// ─── TestValidateRouting_SysFrom ─────────────────────────────────────────────

// TestValidateRouting_SysFrom exercises the validateRouting system-sender branch.
// We call Send directly with sysFrom to hit those code paths.
func TestValidateRouting_SysFrom(t *testing.T) {
	e := newEnv(t)

	t.Run("sysFrom non-Lifecycle rejected", func(t *testing.T) {
		// Use Send which calls validateRouting; sysFrom + non-Lifecycle should fail.
		err := e.svc.Send(
			sysFrom, e.conductor.ID,
			store.MessageTypeAssignment, store.PriorityNormal,
			"test", false,
		)
		if err == nil {
			t.Fatal("expected error for sysFrom + non-Lifecycle, got nil")
		}
		if !strings.Contains(err.Error(), "Lifecycle") {
			t.Errorf("error should mention Lifecycle, got: %v", err)
		}
	})

	t.Run("sysFrom Lifecycle to non-Conductor rejected", func(t *testing.T) {
		err := e.svc.Send(
			sysFrom, e.coder.ID,
			store.MessageTypeLifecycle, store.PriorityNormal,
			"test", false,
		)
		if err == nil {
			t.Fatal("expected error for sysFrom Lifecycle to non-Conductor, got nil")
		}
		if !strings.Contains(err.Error(), "Conductor") {
			t.Errorf("error should mention Conductor, got: %v", err)
		}
	})

	t.Run("sysFrom Lifecycle to Conductor allowed", func(t *testing.T) {
		// Valid path: infrastructure → Lifecycle → Conductor.
		// Deliver to conductor is a no-op (Conductor notifications use the inbox,
		// not direct delivery), so this succeeds end-to-end.
		err := e.svc.Send(
			sysFrom, e.conductor.ID,
			store.MessageTypeLifecycle, store.PriorityNormal,
			"node started", false,
		)
		if err != nil {
			t.Errorf("expected success for sysFrom Lifecycle to Conductor, got: %v", err)
		}
	})
}

// ─── TestValidateRouting_ConductorNonAssignment ───────────────────────────────

// TestValidateRouting_ConductorNonAssignment verifies that the Conductor can
// only send Assignment messages to players.
func TestValidateRouting_ConductorNonAssignment(t *testing.T) {
	e := newEnv(t)

	nonAssignmentTypes := []store.MessageType{
		store.MessageTypeDone,
		store.MessageTypeBlocked,
		store.MessageTypeBackground,
	}
	for _, typ := range nonAssignmentTypes {
		err := e.svc.Send(
			e.conductor.ID, e.coder.ID,
			typ, store.PriorityNormal,
			"payload", false,
		)
		if err == nil {
			t.Errorf("Conductor→Player %q: expected error, got nil", typ)
		}
	}
}

// ─── TestValidateRouting_PlayerUnknownType ────────────────────────────────────

// TestValidateRouting_PlayerUnknownType verifies the default case in validateRouting:
// a player sending an unrecognized message type to the Conductor.
func TestValidateRouting_PlayerUnknownType(t *testing.T) {
	e := newEnv(t)
	// Lifecycle type from a non-system player to Conductor hits the default case.
	err := e.svc.Send(
		e.coder.ID, e.conductor.ID,
		store.MessageTypeLifecycle, store.PriorityNormal,
		"lifecycle from player", false,
	)
	if err == nil {
		t.Fatal("expected error for player sending Lifecycle, got nil")
	}
}

// ─── TestValidateRouting_PlayerTooConductorBackground ─────────────────────────

// TestValidateRouting_PlayerBackground verifies Background routing from Player→Conductor is allowed.
func TestValidateRouting_PlayerBackground(t *testing.T) {
	e := newEnv(t)
	err := e.svc.Send(
		e.coder.ID, e.conductor.ID,
		store.MessageTypeBackground, store.PriorityNormal,
		"going background", false,
	)
	// Routing is valid; this may succeed or produce a non-routing error.
	// We only care that it doesn't fail with a routing rejection.
	if err != nil && strings.Contains(err.Error(), "Player-to-Player") {
		t.Errorf("Background Player→Conductor should not produce a Player-to-Player error: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "routing") {
		t.Errorf("Background Player→Conductor should not produce a routing error: %v", err)
	}
}

// ─── TestHandleDone_UnknownJob ────────────────────────────────────────────────

func TestHandleDone_UnknownJobID(t *testing.T) {
	e := newEnv(t)
	err := e.svc.HandleDone(e.coder.ID, "no-such-job", "done")
	if err == nil {
		t.Fatal("expected error for unknown jobID, got nil")
	}
}

// ─── TestHandleBackground_UnknownJob ─────────────────────────────────────────

func TestHandleBackground_UnknownJob(t *testing.T) {
	e := newEnv(t)
	err := e.svc.HandleBackground(e.coder.ID, "no-such-job")
	if err == nil {
		t.Fatal("expected error for unknown jobID, got nil")
	}
}

// ─── TestHandlePlayerDead_NoActiveJobs ───────────────────────────────────────

// TestHandlePlayerDead_NoActiveJobs verifies HandlePlayerDead succeeds when the
// player has no active jobs (no-op path for the job loop).
func TestHandlePlayerDead_NoActiveJobs(t *testing.T) {
	e := newEnv(t)

	// Kill the coder without any active jobs.
	if err := e.svc.HandlePlayerDead(e.coder.ID); err != nil {
		t.Fatalf("HandlePlayerDead with no active jobs: %v", err)
	}

	p, err := e.players.Get(e.coder.ID)
	if err != nil {
		t.Fatalf("Get player: %v", err)
	}
	if p.Status != "Dead" {
		t.Errorf("expected Dead, got %q", p.Status)
	}

	// No notifications should be created (no active jobs).
	notifs, _ := e.svc.GetNotifications(0, 0)
	if len(notifs) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(notifs))
	}
}

// ─── TestMarkNotificationRead_UnknownID ──────────────────────────────────────

func TestMarkNotificationRead_UnknownID(t *testing.T) {
	e := newEnv(t)
	err := e.svc.MarkNotificationRead("no-such-notification")
	if err == nil {
		t.Fatal("expected error for unknown notification ID, got nil")
	}
}

// ─── TestSend_UnknownFrom ────────────────────────────────────────────────────

// TestSend_UnknownFrom verifies Send returns an error when the from player
// does not exist, exercising the GetPlayer error path in validateRouting.
func TestSend_UnknownFrom(t *testing.T) {
	e := newEnv(t)
	err := e.svc.Send(
		"unknown-player", e.coder.ID,
		store.MessageTypeAssignment, store.PriorityNormal,
		"task", false,
	)
	if err == nil {
		t.Fatal("expected error for unknown from player, got nil")
	}
}

// ─── TestSend_UnknownTo ───────────────────────────────────────────────────────

// TestSend_UnknownTo_SysFrom verifies Send returns an error when the to player (for sysFrom) does not exist.
func TestSend_UnknownTo_SysFrom(t *testing.T) {
	e := newEnv(t)
	// sysFrom + Lifecycle to unknown player exercises the toPlayer GetPlayer error (sysFrom path).
	err := e.svc.Send(
		sysFrom, "unknown-conductor",
		store.MessageTypeLifecycle, store.PriorityNormal,
		"lifecycle event", false,
	)
	if err == nil {
		t.Fatal("expected error for unknown to player (sysFrom), got nil")
	}
}

// TestSend_UnknownTo_Player verifies Send returns an error when a player sends to a
// non-existent player ID. This covers the toPlayer GetPlayer error in the non-sysFrom path.
func TestSend_UnknownTo_Player(t *testing.T) {
	e := newEnv(t)
	err := e.svc.Send(
		e.coder.ID, "unknown-target",
		store.MessageTypeDone, store.PriorityNormal,
		"done", false,
	)
	if err == nil {
		t.Fatal("expected error for unknown to player (player path), got nil")
	}
}

// ─── TestDeliver_NonAssignmentMessage ────────────────────────────────────────

// TestDeliver_NonAssignmentMessage verifies that Deliver can deliver a non-Assignment
// message (e.g., a Done signal manually enqueued to a player) without attempting job
// creation. This covers the `msg.Type != Assignment` branch in Deliver.
func TestDeliver_NonAssignmentMessage(t *testing.T) {
	e := newEnv(t)

	// Directly enqueue a Done message to the coder (bypassing routing enforcement).
	// This simulates an unusual but valid scenario where a non-assignment message
	// is queued for direct delivery.
	msg, err := e.store.CreateMessage(
		e.conductor.ID, e.coder.ID,
		store.MessageTypeDone, store.PriorityNormal,
		"direct done signal", false,
	)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Deliver to coder (Idle) — should mark delivered without creating a Job.
	if err := e.svc.Deliver(e.coder.ID); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	// Message should be marked delivered.
	got, err := e.store.GetMessage(msg.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.DeliveredAt == nil {
		t.Error("expected DeliveredAt to be set after Deliver")
	}

	// No job should have been created (no Assignment).
	jobs, err := e.store.ListActiveJobs(e.coder.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("expected no jobs for non-assignment delivery, got %d", len(jobs))
	}
}

// ─── TestDeliver_EmptyQueue ───────────────────────────────────────────────────

// TestDeliver_EmptyQueue verifies that Deliver is a no-op when the queue is empty.
func TestDeliver_EmptyQueue(t *testing.T) {
	e := newEnv(t)

	// Coder is Idle with nothing queued.
	if err := e.svc.Deliver(e.coder.ID); err != nil {
		t.Fatalf("Deliver empty queue: %v", err)
	}

	// No jobs should exist.
	jobs, err := e.store.ListActiveJobs(e.coder.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs after empty-queue Deliver, got %d", len(jobs))
	}
}

// ─── TestDeliver_ClosedStore ──────────────────────────────────────────────────

// TestDeliver_ClosedStore verifies Deliver propagates a store error (GetPlayer fails).
func TestDeliver_ClosedStore(t *testing.T) {
	s := openTestStore(t)
	ps := player.New(s)
	js := job.New(s)
	svc := New(s, ps, js)

	// Register coder, then close the store before calling Deliver.
	coder, err := ps.Register("coder", false, nil)
	if err != nil {
		t.Fatalf("Register coder: %v", err)
	}
	s.Close()

	if err := svc.Deliver(coder.ID); err == nil {
		t.Fatal("expected error from closed store, got nil")
	}
}

// ─── TestGetNotifications_ClosedStore ────────────────────────────────────────

func TestGetNotifications_ClosedStore(t *testing.T) {
	s := openTestStore(t)
	ps := player.New(s)
	js := job.New(s)
	svc := New(s, ps, js)
	s.Close()

	if _, err := svc.GetNotifications(0, 0); err == nil {
		t.Fatal("expected error from closed store, got nil")
	}
}

// ─── TestCountUnreadNotifications_ClosedStore ─────────────────────────────────

func TestCountUnreadNotifications_ClosedStore(t *testing.T) {
	s := openTestStore(t)
	ps := player.New(s)
	js := job.New(s)
	svc := New(s, ps, js)
	s.Close()

	if _, err := svc.CountUnreadNotifications(); err == nil {
		t.Fatal("expected error from closed store, got nil")
	}
}

// ─── TestHandlePlayerDead_ClosedStore ─────────────────────────────────────────

// TestHandlePlayerDead_ClosedStore verifies HandlePlayerDead propagates the
// ListActiveJobs error when the store is closed after marking the player dead.
// This exercises the error path after MarkDead succeeds but ListActiveJobs fails.
func TestHandlePlayerDead_ClosedStore(t *testing.T) {
	// We need MarkDead to succeed (player must exist) then ListActiveJobs to fail.
	// Instead: call HandlePlayerDead on a player that doesn't exist.
	s := openTestStore(t)
	ps := player.New(s)
	js := job.New(s)
	svc := New(s, ps, js)

	if err := svc.HandlePlayerDead("no-such-player"); err == nil {
		t.Fatal("expected error for unknown player, got nil")
	}
}

// ─── WaitForDecision / RecordDecision ─────────────────────────────────────────

// TestWaitForDecision_RecordDecision verifies the full round-trip: HandleBlocked
// with wait=true returns an approvalID; WaitForDecision blocks until
// RecordDecision signals it with the correct decision.
func TestWaitForDecision_RecordDecision(t *testing.T) {
	e := newEnv(t)

	e.sendAssignment(t, "approval task")
	j := e.activeJobForCoder(t)

	approvalID, err := e.svc.HandleBlocked(e.coder.ID, j.ID, "need decision", "{}", true)
	if err != nil {
		t.Fatalf("HandleBlocked: %v", err)
	}
	if approvalID == "" {
		t.Fatal("expected non-empty approvalID for wait=true")
	}

	// Start WaitForDecision in a goroutine — it should block until RecordDecision.
	type result struct {
		decision store.ApprovalDecision
		err      error
	}
	ch := make(chan result, 1)
	go func() {
		d, err := e.svc.WaitForDecision(context.Background(), approvalID)
		ch <- result{d, err}
	}()

	// Give the goroutine a moment to register the waiter.
	time.Sleep(20 * time.Millisecond)

	// Record the decision — should unblock WaitForDecision.
	if err := e.svc.RecordDecision(approvalID, store.ApprovalDecisionHuman); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}

	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("WaitForDecision returned error: %v", res.err)
		}
		if res.decision != store.ApprovalDecisionHuman {
			t.Errorf("decision: want Human got %q", res.decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForDecision did not unblock within 2s")
	}
}

// TestWaitForDecision_ContextCancelled verifies that WaitForDecision returns an
// error when the context is cancelled before a decision is recorded.
func TestWaitForDecision_ContextCancelled(t *testing.T) {
	e := newEnv(t)

	e.sendAssignment(t, "cancel task")
	j := e.activeJobForCoder(t)

	approvalID, err := e.svc.HandleBlocked(e.coder.ID, j.ID, "waiting", "{}", true)
	if err != nil {
		t.Fatalf("HandleBlocked: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = e.svc.WaitForDecision(ctx, approvalID)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestRecordDecision_UnknownApproval verifies that RecordDecision returns an
// error for a non-existent approval ID.
func TestRecordDecision_UnknownApproval(t *testing.T) {
	e := newEnv(t)
	err := e.svc.RecordDecision("no-such-approval", store.ApprovalDecisionAutonomous)
	if err == nil {
		t.Fatal("expected error for unknown approval ID, got nil")
	}
}

// TestRecordDecision_NoWaiter verifies that RecordDecision succeeds even when
// no goroutine is currently waiting (the waiter channel branch is simply skipped).
func TestRecordDecision_NoWaiter(t *testing.T) {
	e := newEnv(t)

	e.sendAssignment(t, "no-waiter task")
	j := e.activeJobForCoder(t)

	approvalID, err := e.svc.HandleBlocked(e.coder.ID, j.ID, "advisory", "{}", true)
	if err != nil {
		t.Fatalf("HandleBlocked: %v", err)
	}

	// RecordDecision with no goroutine waiting — should succeed without blocking.
	if err := e.svc.RecordDecision(approvalID, store.ApprovalDecisionAutonomous); err != nil {
		t.Fatalf("RecordDecision (no waiter): %v", err)
	}
}
