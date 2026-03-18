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

// TestJobs_SetScratchpad verifies SetJobScratchpad updates the path.
func TestJobs_SetScratchpad(t *testing.T) {
	s := openTestStore(t)

	msg, _ := s.CreateMessage("conductor", "coder", MessageTypeAssignment, PriorityNormal, "work", false)
	player, _ := s.CreatePlayer("coder", false)
	job, _ := s.CreateJob(msg.ID, player.ID, "coder", "work", "")

	newPath := "/home/user/.maestro/scratch/" + job.ID + ".md"
	if err := s.SetJobScratchpad(job.ID, newPath); err != nil {
		t.Fatalf("SetJobScratchpad: %v", err)
	}

	got, err := s.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ScratchpadPath != newPath {
		t.Errorf("ScratchpadPath = %q, want %q", got.ScratchpadPath, newPath)
	}
}

// TestJobs_SetJobDeadLetter verifies SetJobDeadLetter transitions status and stamps completed_at.
func TestJobs_SetJobDeadLetter(t *testing.T) {
	s := openTestStore(t)

	msg, _ := s.CreateMessage("conductor", "coder", MessageTypeAssignment, PriorityNormal, "work", false)
	player, _ := s.CreatePlayer("coder", false)
	job, _ := s.CreateJob(msg.ID, player.ID, "coder", "work", "/tmp/scratch/test.md")

	if err := s.SetJobDeadLetter(job.ID); err != nil {
		t.Fatalf("SetJobDeadLetter: %v", err)
	}

	got, err := s.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != JobStatusDeadLetter {
		t.Errorf("Status = %q, want DeadLetter", got.Status)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set after SetJobDeadLetter")
	}
}

// TestJobs_SetJobPayload verifies SetJobPayload overwrites the payload field.
func TestJobs_SetJobPayload(t *testing.T) {
	s := openTestStore(t)

	msg, _ := s.CreateMessage("conductor", "coder", MessageTypeAssignment, PriorityNormal, "original", false)
	player, _ := s.CreatePlayer("coder", false)
	job, _ := s.CreateJob(msg.ID, player.ID, "coder", "original", "")

	injected := "original\n\n$MAESTRO_JOB_ID=" + job.ID + "\n$MAESTRO_SCRATCHPAD=/tmp/test.md"
	if err := s.SetJobPayload(job.ID, injected); err != nil {
		t.Fatalf("SetJobPayload: %v", err)
	}

	got, err := s.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Payload != injected {
		t.Errorf("Payload = %q, want %q", got.Payload, injected)
	}
}

// TestJobs_ListJobs verifies ListJobs returns all jobs ordered by creation time.
func TestJobs_ListJobs(t *testing.T) {
	s := openTestStore(t)

	p, _ := s.CreatePlayer("coder", false)
	m1, _ := s.CreateMessage("conductor", p.ID, MessageTypeAssignment, PriorityNormal, "w1", false)
	m2, _ := s.CreateMessage("conductor", p.ID, MessageTypeAssignment, PriorityNormal, "w2", false)
	j1, _ := s.CreateJob(m1.ID, p.ID, "coder", "w1", "")
	j2, _ := s.CreateJob(m2.ID, p.ID, "coder", "w2", "")

	jobs, err := s.ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("ListJobs len = %d, want 2", len(jobs))
	}
	ids := map[string]bool{jobs[0].ID: true, jobs[1].ID: true}
	if !ids[j1.ID] || !ids[j2.ID] {
		t.Errorf("ListJobs returned unexpected IDs: %v %v", jobs[0].ID, jobs[1].ID)
	}
}

// TestJobs_ListJobsByPlayer verifies ListJobsByPlayer filters by player_id.
func TestJobs_ListJobsByPlayer(t *testing.T) {
	s := openTestStore(t)

	pA, _ := s.CreatePlayer("coder-A", false)
	pB, _ := s.CreatePlayer("coder-B", false)
	mA1, _ := s.CreateMessage("conductor", pA.ID, MessageTypeAssignment, PriorityNormal, "wA1", false)
	mA2, _ := s.CreateMessage("conductor", pA.ID, MessageTypeAssignment, PriorityNormal, "wA2", false)
	mB1, _ := s.CreateMessage("conductor", pB.ID, MessageTypeAssignment, PriorityNormal, "wB1", false)

	jA1, _ := s.CreateJob(mA1.ID, pA.ID, "coder-A", "wA1", "")
	jA2, _ := s.CreateJob(mA2.ID, pA.ID, "coder-A", "wA2", "")
	_, _ = s.CreateJob(mB1.ID, pB.ID, "coder-B", "wB1", "")

	jobs, err := s.ListJobsByPlayer(pA.ID)
	if err != nil {
		t.Fatalf("ListJobsByPlayer: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("ListJobsByPlayer len = %d, want 2", len(jobs))
	}
	ids := map[string]bool{jobs[0].ID: true, jobs[1].ID: true}
	if !ids[jA1.ID] || !ids[jA2.ID] {
		t.Errorf("ListJobsByPlayer returned wrong jobs")
	}
	for _, j := range jobs {
		if j.PlayerID != pA.ID {
			t.Errorf("expected player %q, got %q", pA.ID, j.PlayerID)
		}
	}
}

// TestJobs_ListJobsByStatus verifies ListJobsByStatus filters by status.
func TestJobs_ListJobsByStatus(t *testing.T) {
	s := openTestStore(t)

	p, _ := s.CreatePlayer("coder", false)
	m1, _ := s.CreateMessage("conductor", p.ID, MessageTypeAssignment, PriorityNormal, "w1", false)
	m2, _ := s.CreateMessage("conductor", p.ID, MessageTypeAssignment, PriorityNormal, "w2", false)
	m3, _ := s.CreateMessage("conductor", p.ID, MessageTypeAssignment, PriorityNormal, "w3", false)
	j1, _ := s.CreateJob(m1.ID, p.ID, "coder", "w1", "")
	_, _ = s.CreateJob(m2.ID, p.ID, "coder", "w2", "")
	j3, _ := s.CreateJob(m3.ID, p.ID, "coder", "w3", "")

	_ = s.UpdateJobStatus(j3.ID, JobStatusBackgrounded)

	inProgress, err := s.ListJobsByStatus(JobStatusInProgress)
	if err != nil {
		t.Fatalf("ListJobsByStatus InProgress: %v", err)
	}
	if len(inProgress) != 2 {
		t.Errorf("ListJobsByStatus InProgress len = %d, want 2", len(inProgress))
	}

	backgrounded, err := s.ListJobsByStatus(JobStatusBackgrounded)
	if err != nil {
		t.Fatalf("ListJobsByStatus Backgrounded: %v", err)
	}
	if len(backgrounded) != 1 || backgrounded[0].ID != j3.ID {
		t.Errorf("ListJobsByStatus Backgrounded: expected j3, got %+v", backgrounded)
	}

	complete, err := s.ListJobsByStatus(JobStatusComplete)
	if err != nil {
		t.Fatalf("ListJobsByStatus Complete: %v", err)
	}
	if len(complete) != 0 {
		t.Errorf("ListJobsByStatus Complete len = %d, want 0", len(complete))
	}
	_ = j1 // suppress unused warning
}

// TestJobs_ListJobsByPlayerAndStatus verifies combined player+status filtering.
func TestJobs_ListJobsByPlayerAndStatus(t *testing.T) {
	s := openTestStore(t)

	pA, _ := s.CreatePlayer("coder-A", false)
	pB, _ := s.CreatePlayer("coder-B", false)
	mA1, _ := s.CreateMessage("conductor", pA.ID, MessageTypeAssignment, PriorityNormal, "wA1", false)
	mA2, _ := s.CreateMessage("conductor", pA.ID, MessageTypeAssignment, PriorityNormal, "wA2", false)
	mB1, _ := s.CreateMessage("conductor", pB.ID, MessageTypeAssignment, PriorityNormal, "wB1", false)

	jA1, _ := s.CreateJob(mA1.ID, pA.ID, "coder-A", "wA1", "")
	jA2, _ := s.CreateJob(mA2.ID, pA.ID, "coder-A", "wA2", "")
	_, _ = s.CreateJob(mB1.ID, pB.ID, "coder-B", "wB1", "")

	// Background jA2 for pA.
	_ = s.UpdateJobStatus(jA2.ID, JobStatusBackgrounded)

	// pA InProgress → should be only jA1.
	inProgress, err := s.ListJobsByPlayerAndStatus(pA.ID, JobStatusInProgress)
	if err != nil {
		t.Fatalf("ListJobsByPlayerAndStatus InProgress: %v", err)
	}
	if len(inProgress) != 1 || inProgress[0].ID != jA1.ID {
		t.Errorf("expected only jA1 InProgress, got %+v", inProgress)
	}

	// pA Backgrounded → should be only jA2.
	backgrounded, err := s.ListJobsByPlayerAndStatus(pA.ID, JobStatusBackgrounded)
	if err != nil {
		t.Fatalf("ListJobsByPlayerAndStatus Backgrounded: %v", err)
	}
	if len(backgrounded) != 1 || backgrounded[0].ID != jA2.ID {
		t.Errorf("expected only jA2 Backgrounded, got %+v", backgrounded)
	}

	// pB InProgress → none (pB job not Backgrounded, but confirm it's isolated to pB).
	pBJobs, err := s.ListJobsByPlayerAndStatus(pB.ID, JobStatusInProgress)
	if err != nil {
		t.Fatalf("ListJobsByPlayerAndStatus pB InProgress: %v", err)
	}
	if len(pBJobs) != 1 {
		t.Errorf("expected 1 InProgress job for pB, got %d", len(pBJobs))
	}
}

// TestNotifications_Paginated verifies ListUnreadNotificationsPaginated with limit and offset.
func TestNotifications_Paginated(t *testing.T) {
	s := openTestStore(t)

	// Create 5 notifications.
	for i := 0; i < 5; i++ {
		if _, err := s.CreateNotification(nil, nil, "test", "summary"); err != nil {
			t.Fatalf("CreateNotification %d: %v", i, err)
		}
	}

	// limit=0 → all 5.
	all, err := s.ListUnreadNotificationsPaginated(0, 0)
	if err != nil {
		t.Fatalf("ListUnreadNotificationsPaginated(0,0): %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("limit=0 expected 5, got %d", len(all))
	}

	// limit=3, offset=0 → first 3.
	page1, err := s.ListUnreadNotificationsPaginated(3, 0)
	if err != nil {
		t.Fatalf("ListUnreadNotificationsPaginated(3,0): %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("page1 expected 3, got %d", len(page1))
	}

	// limit=3, offset=3 → last 2.
	page2, err := s.ListUnreadNotificationsPaginated(3, 3)
	if err != nil {
		t.Fatalf("ListUnreadNotificationsPaginated(3,3): %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 expected 2, got %d", len(page2))
	}

	// Pages should not share IDs.
	p1IDs := map[string]bool{}
	for _, n := range page1 {
		p1IDs[n.ID] = true
	}
	for _, n := range page2 {
		if p1IDs[n.ID] {
			t.Errorf("notification %q appears in both pages", n.ID)
		}
	}
}

// TestNotifications_Count verifies CountUnreadNotifications before and after reads.
func TestNotifications_Count(t *testing.T) {
	s := openTestStore(t)

	count, err := s.CountUnreadNotifications()
	if err != nil {
		t.Fatalf("CountUnreadNotifications (empty): %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	n1, _ := s.CreateNotification(nil, nil, "test", "first")
	_, _ = s.CreateNotification(nil, nil, "test", "second")

	count, err = s.CountUnreadNotifications()
	if err != nil {
		t.Fatalf("CountUnreadNotifications: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2, got %d", count)
	}

	_ = s.MarkNotificationRead(n1.ID)

	count, err = s.CountUnreadNotifications()
	if err != nil {
		t.Fatalf("CountUnreadNotifications after read: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 after reading one, got %d", count)
	}
}

// TestPlayers_GetByName verifies GetPlayerByName returns the correct player.
func TestPlayers_GetByName(t *testing.T) {
	s := openTestStore(t)

	p, err := s.CreatePlayer("named-player", false)
	if err != nil {
		t.Fatalf("CreatePlayer: %v", err)
	}

	got, err := s.GetPlayerByName("named-player")
	if err != nil {
		t.Fatalf("GetPlayerByName: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("ID = %q, want %q", got.ID, p.ID)
	}
	if got.Name != "named-player" {
		t.Errorf("Name = %q, want named-player", got.Name)
	}

	// Unknown name should return an error.
	if _, err := s.GetPlayerByName("does-not-exist"); err == nil {
		t.Error("expected error for unknown name, got nil")
	}
}

// TestJobs_NotFound verifies that update/set operations return "not found" errors
// for non-existent job IDs, exercising the RowsAffected == 0 branches.
func TestJobs_NotFound(t *testing.T) {
	s := openTestStore(t)

	t.Run("UpdateJobStatus", func(t *testing.T) {
		if err := s.UpdateJobStatus("no-such-job", JobStatusComplete); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("SetJobCompleted", func(t *testing.T) {
		if err := s.SetJobCompleted("no-such-job"); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("SetJobScratchpad", func(t *testing.T) {
		if err := s.SetJobScratchpad("no-such-job", "/tmp/x.md"); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("SetJobDeadLetter", func(t *testing.T) {
		if err := s.SetJobDeadLetter("no-such-job"); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("SetJobPayload", func(t *testing.T) {
		if err := s.SetJobPayload("no-such-job", "payload"); err == nil {
			t.Error("expected error, got nil")
		}
	})
}

// TestPlayers_NotFound verifies that update operations return "not found" errors
// for non-existent player IDs.
func TestPlayers_NotFound(t *testing.T) {
	s := openTestStore(t)

	t.Run("UpdatePlayerStatus", func(t *testing.T) {
		if err := s.UpdatePlayerStatus("no-such-player", PlayerStatusRunning); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("UpdateLastSeen", func(t *testing.T) {
		if err := s.UpdateLastSeen("no-such-player"); err == nil {
			t.Error("expected error, got nil")
		}
	})
}

// TestMessages_NotFound verifies MarkDelivered returns "not found" for unknown ID.
func TestMessages_NotFound(t *testing.T) {
	s := openTestStore(t)
	if err := s.MarkDelivered("no-such-message"); err == nil {
		t.Error("expected error, got nil")
	}
}

// TestNotifications_NotFound verifies MarkNotificationRead returns "not found" for unknown ID.
func TestNotifications_NotFound(t *testing.T) {
	s := openTestStore(t)
	if err := s.MarkNotificationRead("no-such-notification"); err == nil {
		t.Error("expected error, got nil")
	}
}

// TestCronJobs_NotFound verifies DeleteCronJob and UpdateCronJobFired return
// "not found" for non-existent IDs.
func TestCronJobs_NotFound(t *testing.T) {
	s := openTestStore(t)

	t.Run("DeleteCronJob", func(t *testing.T) {
		if err := s.DeleteCronJob("no-such-cron"); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("UpdateCronJobFired", func(t *testing.T) {
		if err := s.UpdateCronJobFired("no-such-cron", time.Now(), time.Now().Add(time.Hour)); err == nil {
			t.Error("expected error, got nil")
		}
	})
}

// TestApprovals_RecordDecision_NotFound verifies RecordDecision returns "not found".
func TestApprovals_RecordDecision_NotFound(t *testing.T) {
	s := openTestStore(t)
	if err := s.RecordDecision("no-such-approval", ApprovalDecisionHuman); err == nil {
		t.Error("expected error for non-existent approval, got nil")
	}
}

// TestJobs_ClosedStore verifies that List functions return errors when the DB is closed.
// These tests cover the Query error branches that cannot be triggered with a healthy DB.
func TestJobs_ClosedStore(t *testing.T) {
	cases := []struct {
		name string
		fn   func(s *Store) error
	}{
		{"ListJobs", func(s *Store) error {
			_, err := s.ListJobs()
			return err
		}},
		{"ListJobsByPlayer", func(s *Store) error {
			_, err := s.ListJobsByPlayer("p1")
			return err
		}},
		{"ListJobsByStatus", func(s *Store) error {
			_, err := s.ListJobsByStatus(JobStatusInProgress)
			return err
		}},
		{"ListJobsByPlayerAndStatus", func(s *Store) error {
			_, err := s.ListJobsByPlayerAndStatus("p1", JobStatusInProgress)
			return err
		}},
		{"ListDeadLetterJobs", func(s *Store) error {
			_, err := s.ListDeadLetterJobs()
			return err
		}},
		{"ListActiveJobs", func(s *Store) error {
			_, err := s.ListActiveJobs("p1")
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.db")
			s, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			s.Close()
			if err := tc.fn(s); err == nil {
				t.Errorf("%s: expected error from closed DB, got nil", tc.name)
			}
		})
	}
}

// TestPlayers_ClosedStore verifies ListPlayers returns an error when the DB is closed.
func TestPlayers_ClosedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()
	if _, err := s.ListPlayers(); err == nil {
		t.Error("expected error from closed DB, got nil")
	}
}

// TestMessages_ClosedStore verifies ListUndelivered returns an error when the DB is closed.
func TestMessages_ClosedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()
	if _, err := s.ListUndelivered("coder"); err == nil {
		t.Error("expected error from closed DB, got nil")
	}
}

// TestNotifications_ClosedStore verifies List and Count operations return errors on closed DB.
func TestNotifications_ClosedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()

	if _, err := s.ListUnreadNotifications(); err == nil {
		t.Error("ListUnreadNotifications: expected error from closed DB, got nil")
	}
	if _, err := s.ListUnreadNotificationsPaginated(0, 0); err == nil {
		t.Error("ListUnreadNotificationsPaginated: expected error from closed DB, got nil")
	}
	if _, err := s.CountUnreadNotifications(); err == nil {
		t.Error("CountUnreadNotifications: expected error from closed DB, got nil")
	}
}

// TestApprovals_ClosedStore verifies ListPendingApprovals returns an error on closed DB.
func TestApprovals_ClosedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()
	if _, err := s.ListPendingApprovals(); err == nil {
		t.Error("expected error from closed DB, got nil")
	}
}

// TestCronJobs_ClosedStore verifies ListCronJobs returns an error on closed DB.
func TestCronJobs_ClosedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()
	if _, err := s.ListCronJobs(); err == nil {
		t.Error("expected error from closed DB, got nil")
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
