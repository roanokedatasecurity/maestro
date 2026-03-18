package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestOpen_AllTablesCreated verifies that all 5 tables plus schema_migrations
// are present after Open on a fresh database.
func TestOpen_AllTablesCreated(t *testing.T) {
	s := openTestStore(t)

	want := []string{"schema_migrations", "messages", "players", "jobs", "notifications", "approvals", "cron_jobs"}
	for _, table := range want {
		var name string
		err := s.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

// TestOpen_Idempotent verifies that opening the same database twice does not error.
func TestOpen_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idem.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	s2.Close()
}

// TestMessages_CRUD covers create, get, mark delivered, and list undelivered.
func TestMessages_CRUD(t *testing.T) {
	s := openTestStore(t)

	msg, err := s.CreateMessage("conductor", "coder", MessageTypeAssignment, PriorityHigh, `{"task":"implement foo"}`, true)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if msg.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if msg.Type != MessageTypeAssignment {
		t.Errorf("Type = %q, want %q", msg.Type, MessageTypeAssignment)
	}
	if msg.Priority != PriorityHigh {
		t.Errorf("Priority = %q, want %q", msg.Priority, PriorityHigh)
	}
	if !msg.WaitForAck {
		t.Error("WaitForAck should be true")
	}
	if msg.DeliveredAt != nil {
		t.Error("DeliveredAt should be nil initially")
	}

	got, err := s.GetMessage(msg.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.ID != msg.ID {
		t.Errorf("GetMessage ID = %q, want %q", got.ID, msg.ID)
	}

	undelivered, err := s.ListUndelivered("coder")
	if err != nil {
		t.Fatalf("ListUndelivered: %v", err)
	}
	if len(undelivered) != 1 {
		t.Fatalf("ListUndelivered len = %d, want 1", len(undelivered))
	}

	if err := s.MarkDelivered(msg.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	delivered, err := s.GetMessage(msg.ID)
	if err != nil {
		t.Fatalf("GetMessage after deliver: %v", err)
	}
	if delivered.DeliveredAt == nil {
		t.Error("DeliveredAt should be set after MarkDelivered")
	}

	undelivered2, err := s.ListUndelivered("coder")
	if err != nil {
		t.Fatalf("ListUndelivered after deliver: %v", err)
	}
	if len(undelivered2) != 0 {
		t.Errorf("ListUndelivered after deliver len = %d, want 0", len(undelivered2))
	}
}

// TestMessages_PriorityOrdering verifies High messages sort before Normal.
func TestMessages_PriorityOrdering(t *testing.T) {
	s := openTestStore(t)

	_, err := s.CreateMessage("conductor", "coder", MessageTypeAssignment, PriorityNormal, "low", false)
	if err != nil {
		t.Fatalf("CreateMessage normal: %v", err)
	}
	_, err = s.CreateMessage("conductor", "coder", MessageTypeAssignment, PriorityHigh, "high", false)
	if err != nil {
		t.Fatalf("CreateMessage high: %v", err)
	}

	msgs, err := s.ListUndelivered("coder")
	if err != nil {
		t.Fatalf("ListUndelivered: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2", len(msgs))
	}
	if msgs[0].Priority != PriorityHigh {
		t.Errorf("first message Priority = %q, want High", msgs[0].Priority)
	}
}

// TestTimeFieldsNonZero guards against the parseTime regression where the
// modernc SQLite driver emits RFC3339 ("2006-01-02T15:04:05Z") but the
// original parseTime only accepted "2006-01-02 15:04:05", silently returning
// time.Time{} for every CreatedAt / LastSeenAt field.
func TestTimeFieldsNonZero(t *testing.T) {
	s := openTestStore(t)

	p, err := s.CreatePlayer("time-check", false)
	if err != nil {
		t.Fatalf("CreatePlayer: %v", err)
	}

	got, err := s.GetPlayer(p.ID)
	if err != nil {
		t.Fatalf("GetPlayer: %v", err)
	}

	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero — parseTime failed to parse the driver's datetime format")
	}
	if got.LastSeenAt.IsZero() {
		t.Error("LastSeenAt is zero — parseTime failed to parse the driver's datetime format")
	}
}

// TestPlayers_CRUD covers create, get, update status, last seen, and list.
func TestPlayers_CRUD(t *testing.T) {
	s := openTestStore(t)

	p, err := s.CreatePlayer("researcher", false)
	if err != nil {
		t.Fatalf("CreatePlayer: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if p.Status != PlayerStatusIdle {
		t.Errorf("Status = %q, want Idle", p.Status)
	}
	if p.IsConductor {
		t.Error("IsConductor should be false")
	}

	got, err := s.GetPlayer(p.ID)
	if err != nil {
		t.Fatalf("GetPlayer: %v", err)
	}
	if got.Name != "researcher" {
		t.Errorf("Name = %q, want researcher", got.Name)
	}

	if err := s.UpdatePlayerStatus(p.ID, PlayerStatusRunning); err != nil {
		t.Fatalf("UpdatePlayerStatus: %v", err)
	}
	updated, err := s.GetPlayer(p.ID)
	if err != nil {
		t.Fatalf("GetPlayer after status update: %v", err)
	}
	if updated.Status != PlayerStatusRunning {
		t.Errorf("Status = %q, want Running", updated.Status)
	}

	if err := s.UpdateLastSeen(p.ID); err != nil {
		t.Fatalf("UpdateLastSeen: %v", err)
	}

	players, err := s.ListPlayers()
	if err != nil {
		t.Fatalf("ListPlayers: %v", err)
	}
	if len(players) != 1 {
		t.Fatalf("ListPlayers len = %d, want 1", len(players))
	}
}

// TestPlayers_ConductorUniqueness verifies that a second live Conductor is rejected.
func TestPlayers_ConductorUniqueness(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.CreatePlayer("conductor", true); err != nil {
		t.Fatalf("first Conductor: %v", err)
	}
	if _, err := s.CreatePlayer("conductor-2", true); err == nil {
		t.Error("expected error creating second live Conductor, got nil")
	}
}

// TestPlayers_ConductorAllowedAfterDead verifies a new Conductor can be created
// once the prior one is Dead.
func TestPlayers_ConductorAllowedAfterDead(t *testing.T) {
	s := openTestStore(t)

	first, err := s.CreatePlayer("conductor", true)
	if err != nil {
		t.Fatalf("CreatePlayer: %v", err)
	}
	if err := s.UpdatePlayerStatus(first.ID, PlayerStatusDead); err != nil {
		t.Fatalf("UpdatePlayerStatus Dead: %v", err)
	}
	if _, err := s.CreatePlayer("conductor-2", true); err != nil {
		t.Errorf("expected success creating Conductor after prior is Dead: %v", err)
	}
}

// TestJobs_Lifecycle covers create, status transitions, completion, and listing.
func TestJobs_Lifecycle(t *testing.T) {
	s := openTestStore(t)

	msg, _ := s.CreateMessage("conductor", "coder", MessageTypeAssignment, PriorityNormal, "work", false)
	player, _ := s.CreatePlayer("coder", false)

	job, err := s.CreateJob(msg.ID, player.ID, "coder", "work", "/tmp/maestro/jobs/test.md")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.Status != JobStatusInProgress {
		t.Errorf("Status = %q, want InProgress", job.Status)
	}
	if job.ScratchpadPath != "/tmp/maestro/jobs/test.md" {
		t.Errorf("ScratchpadPath = %q", job.ScratchpadPath)
	}
	if job.CompletedAt != nil {
		t.Error("CompletedAt should be nil initially")
	}

	// Background it
	if err := s.UpdateJobStatus(job.ID, JobStatusBackgrounded); err != nil {
		t.Fatalf("UpdateJobStatus Backgrounded: %v", err)
	}

	active, err := s.ListActiveJobs(player.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("ListActiveJobs len = %d, want 1", len(active))
	}

	// Resume
	if err := s.UpdateJobStatus(job.ID, JobStatusInProgress); err != nil {
		t.Fatalf("UpdateJobStatus InProgress: %v", err)
	}

	// Complete
	if err := s.SetJobCompleted(job.ID); err != nil {
		t.Fatalf("SetJobCompleted: %v", err)
	}
	completed, err := s.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob after complete: %v", err)
	}
	if completed.Status != JobStatusComplete {
		t.Errorf("Status = %q, want Complete", completed.Status)
	}
	if completed.CompletedAt == nil {
		t.Error("CompletedAt should be set after SetJobCompleted")
	}

	active2, err := s.ListActiveJobs(player.ID)
	if err != nil {
		t.Fatalf("ListActiveJobs after complete: %v", err)
	}
	if len(active2) != 0 {
		t.Errorf("ListActiveJobs after complete len = %d, want 0", len(active2))
	}
}

// TestJobs_DeadLetter verifies dead-letter listing.
func TestJobs_DeadLetter(t *testing.T) {
	s := openTestStore(t)

	msg, _ := s.CreateMessage("conductor", "coder", MessageTypeAssignment, PriorityNormal, "work", false)
	player, _ := s.CreatePlayer("coder", false)
	job, _ := s.CreateJob(msg.ID, player.ID, "coder", "work", "/tmp/maestro/jobs/dl.md")

	if err := s.UpdateJobStatus(job.ID, JobStatusDeadLetter); err != nil {
		t.Fatalf("UpdateJobStatus DeadLetter: %v", err)
	}

	dl, err := s.ListDeadLetterJobs()
	if err != nil {
		t.Fatalf("ListDeadLetterJobs: %v", err)
	}
	if len(dl) != 1 {
		t.Fatalf("ListDeadLetterJobs len = %d, want 1", len(dl))
	}
	if dl[0].ID != job.ID {
		t.Errorf("DeadLetter job ID = %q, want %q", dl[0].ID, job.ID)
	}
}

// TestJobs_ApprovalMetadataRoundTrip verifies the JSON blob column.
func TestJobs_ApprovalMetadataRoundTrip(t *testing.T) {
	s := openTestStore(t)

	msg, _ := s.CreateMessage("conductor", "coder", MessageTypeAssignment, PriorityNormal, "work", false)
	player, _ := s.CreatePlayer("coder", false)
	job, _ := s.CreateJob(msg.ID, player.ID, "coder", "work", "/tmp/maestro/jobs/meta.md")

	meta := map[string]any{"reversibility": "low", "confidence": 0.95}
	metaJSON, _ := json.Marshal(meta)

	if _, err := s.db.Exec(
		"UPDATE jobs SET approval_metadata = ? WHERE id = ?", string(metaJSON), job.ID,
	); err != nil {
		t.Fatalf("set approval_metadata: %v", err)
	}

	got, err := s.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ApprovalMetadata == nil {
		t.Fatal("ApprovalMetadata should not be nil")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(*got.ApprovalMetadata), &decoded); err != nil {
		t.Fatalf("unmarshal ApprovalMetadata: %v", err)
	}
	if decoded["reversibility"] != "low" {
		t.Errorf("reversibility = %v, want low", decoded["reversibility"])
	}
}

// TestNotifications_CRUD covers create, mark read, and list unread.
func TestNotifications_CRUD(t *testing.T) {
	s := openTestStore(t)

	n, err := s.CreateNotification(nil, nil, "session", "Migrated from conn v1")
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if n.ReadAt != nil {
		t.Error("ReadAt should be nil initially")
	}
	if n.MessageID != nil || n.JobID != nil {
		t.Error("MessageID and JobID should be nil")
	}

	unread, err := s.ListUnreadNotifications()
	if err != nil {
		t.Fatalf("ListUnreadNotifications: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("ListUnreadNotifications len = %d, want 1", len(unread))
	}

	if err := s.MarkNotificationRead(n.ID); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}

	got, err := s.GetNotification(n.ID)
	if err != nil {
		t.Fatalf("GetNotification: %v", err)
	}
	if got.ReadAt == nil {
		t.Error("ReadAt should be set after MarkNotificationRead")
	}

	unread2, err := s.ListUnreadNotifications()
	if err != nil {
		t.Fatalf("ListUnreadNotifications after read: %v", err)
	}
	if len(unread2) != 0 {
		t.Errorf("ListUnreadNotifications after read len = %d, want 0", len(unread2))
	}
}

// TestCronJobCRUD covers create, get, list, update fired timestamps, and delete.
func TestCronJobCRUD(t *testing.T) {
	s := openTestStore(t)

	ownerID := "player-abc"
	cj := CronJob{
		ID:             newID(),
		Name:           "nightly-report",
		ScriptPath:     "/scripts/nightly.sh",
		Schedule:       "0 2 * * *",
		ScratchpadPath: "/tmp/maestro/cron/nightly.md",
		OwnerPlayerID:  &ownerID,
	}

	if err := s.CreateCronJob(cj); err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}

	// GetCronJob
	got, err := s.GetCronJob(cj.ID)
	if err != nil {
		t.Fatalf("GetCronJob: %v", err)
	}
	if got.Name != cj.Name {
		t.Errorf("Name = %q, want %q", got.Name, cj.Name)
	}
	if got.Schedule != cj.Schedule {
		t.Errorf("Schedule = %q, want %q", got.Schedule, cj.Schedule)
	}
	if got.OwnerPlayerID == nil || *got.OwnerPlayerID != ownerID {
		t.Errorf("OwnerPlayerID = %v, want %q", got.OwnerPlayerID, ownerID)
	}
	if got.LastFiredAt != nil {
		t.Error("LastFiredAt should be nil initially")
	}
	if got.NextFireAt != nil {
		t.Error("NextFireAt should be nil initially")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	// ListCronJobs
	list, err := s.ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListCronJobs len = %d, want 1", len(list))
	}
	if list[0].ID != cj.ID {
		t.Errorf("list[0].ID = %q, want %q", list[0].ID, cj.ID)
	}

	// UpdateCronJobFired
	lastFired := time.Now().UTC().Truncate(time.Second)
	nextFire := lastFired.Add(24 * time.Hour)
	if err := s.UpdateCronJobFired(cj.ID, lastFired, nextFire); err != nil {
		t.Fatalf("UpdateCronJobFired: %v", err)
	}
	updated, err := s.GetCronJob(cj.ID)
	if err != nil {
		t.Fatalf("GetCronJob after fired update: %v", err)
	}
	if updated.LastFiredAt == nil {
		t.Fatal("LastFiredAt should be set after UpdateCronJobFired")
	}
	if !updated.LastFiredAt.Equal(lastFired) {
		t.Errorf("LastFiredAt = %v, want %v", updated.LastFiredAt, lastFired)
	}
	if updated.NextFireAt == nil {
		t.Fatal("NextFireAt should be set after UpdateCronJobFired")
	}
	if !updated.NextFireAt.Equal(nextFire) {
		t.Errorf("NextFireAt = %v, want %v", updated.NextFireAt, nextFire)
	}

	// DeleteCronJob
	if err := s.DeleteCronJob(cj.ID); err != nil {
		t.Fatalf("DeleteCronJob: %v", err)
	}
	list2, err := s.ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs after delete: %v", err)
	}
	if len(list2) != 0 {
		t.Errorf("ListCronJobs after delete len = %d, want 0", len(list2))
	}

	// GetCronJob on deleted ID should error
	if _, err := s.GetCronJob(cj.ID); err == nil {
		t.Error("GetCronJob on deleted ID should return error")
	}
}

// TestApprovals_CRUD covers create, record decision, and list pending.
func TestApprovals_CRUD(t *testing.T) {
	s := openTestStore(t)

	msg, _ := s.CreateMessage("conductor", "coder", MessageTypeBlocked, PriorityHigh, "need help", true)
	player, _ := s.CreatePlayer("coder", false)
	job, _ := s.CreateJob(msg.ID, player.ID, "coder", "work", "/tmp/maestro/jobs/appr.md")

	scorecard := `{"reversibility":"high","confidence":0.6,"impact_radius":"narrow"}`
	a, err := s.CreateApproval(job.ID, msg.ID, scorecard)
	if err != nil {
		t.Fatalf("CreateApproval: %v", err)
	}
	if a.Decision != nil {
		t.Error("Decision should be nil initially")
	}
	if a.Scorecard != scorecard {
		t.Errorf("Scorecard = %q, want %q", a.Scorecard, scorecard)
	}

	// Verify scorecard JSON is parseable
	var sc map[string]any
	if err := json.Unmarshal([]byte(a.Scorecard), &sc); err != nil {
		t.Fatalf("scorecard JSON invalid: %v", err)
	}

	pending, err := s.ListPendingApprovals()
	if err != nil {
		t.Fatalf("ListPendingApprovals: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("ListPendingApprovals len = %d, want 1", len(pending))
	}

	if err := s.RecordDecision(a.ID, ApprovalDecisionHuman); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}

	decided, err := s.GetApproval(a.ID)
	if err != nil {
		t.Fatalf("GetApproval after decision: %v", err)
	}
	if decided.Decision == nil || *decided.Decision != ApprovalDecisionHuman {
		t.Errorf("Decision = %v, want Human", decided.Decision)
	}
	if decided.DecidedAt == nil {
		t.Error("DecidedAt should be set after RecordDecision")
	}

	pending2, err := s.ListPendingApprovals()
	if err != nil {
		t.Fatalf("ListPendingApprovals after decision: %v", err)
	}
	if len(pending2) != 0 {
		t.Errorf("ListPendingApprovals after decision len = %d, want 0", len(pending2))
	}
}
