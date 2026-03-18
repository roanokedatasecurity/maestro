package store

import (
	"database/sql"
	"fmt"
	"time"
)

type JobStatus string

const (
	JobStatusInProgress   JobStatus = "InProgress"
	JobStatusBackgrounded JobStatus = "Backgrounded"
	JobStatusComplete     JobStatus = "Complete"
	JobStatusDeadLetter   JobStatus = "DeadLetter"
)

type Job struct {
	ID               string
	MessageID        string
	PlayerID         string
	PlayerName       string
	Payload          string
	ScratchpadPath   string
	Status           JobStatus
	ApprovalMetadata *string // JSON blob, reserved
	CreatedAt        time.Time
	CompletedAt      *time.Time
}

// CreateJob creates a new InProgress job for a delivered Assignment. The
// scratchpad path is assigned by the caller (typically internal/job).
func (s *Store) CreateJob(messageID, playerID, playerName, payload, scratchpadPath string) (*Job, error) {
	id := newID()
	_, err := s.db.Exec(`
		INSERT INTO jobs (id, message_id, player_id, player_name, payload, scratchpad_path)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, messageID, playerID, playerName, payload, scratchpadPath,
	)
	if err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	return s.GetJob(id)
}

// GetJob returns a job by ID.
func (s *Store) GetJob(id string) (*Job, error) {
	row := s.db.QueryRow(`
		SELECT id, message_id, player_id, player_name, payload, scratchpad_path,
		       status, approval_metadata, created_at, completed_at
		FROM jobs WHERE id = ?`, id)
	return scanJob(row)
}

// UpdateJobStatus transitions a job to the given status.
// Use SetJobCompleted to transition to Complete — it also stamps completed_at.
func (s *Store) UpdateJobStatus(id string, status JobStatus) error {
	res, err := s.db.Exec(
		"UPDATE jobs SET status = ? WHERE id = ?", string(status), id,
	)
	if err != nil {
		return fmt.Errorf("update job status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("update job status: job %q not found", id)
	}
	return nil
}

// SetJobCompleted transitions a job to Complete and stamps completed_at.
func (s *Store) SetJobCompleted(id string) error {
	res, err := s.db.Exec(
		"UPDATE jobs SET status = 'Complete', completed_at = datetime('now') WHERE id = ?", id,
	)
	if err != nil {
		return fmt.Errorf("set job completed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("set job completed: job %q not found", id)
	}
	return nil
}

// ListActiveJobs returns all InProgress and Backgrounded jobs for a player.
func (s *Store) ListActiveJobs(playerID string) ([]*Job, error) {
	rows, err := s.db.Query(`
		SELECT id, message_id, player_id, player_name, payload, scratchpad_path,
		       status, approval_metadata, created_at, completed_at
		FROM jobs
		WHERE player_id = ? AND status IN ('InProgress','Backgrounded')
		ORDER BY created_at ASC`, playerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list active jobs: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

// ListDeadLetterJobs returns all DeadLetter jobs, ordered oldest first.
func (s *Store) ListDeadLetterJobs() ([]*Job, error) {
	rows, err := s.db.Query(`
		SELECT id, message_id, player_id, player_name, payload, scratchpad_path,
		       status, approval_metadata, created_at, completed_at
		FROM jobs WHERE status = 'DeadLetter' ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list dead letter jobs: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

func scanJob(row *sql.Row) (*Job, error) {
	var j Job
	var status string
	var createdAt string
	var completedAt sql.NullTime
	var approvalMetadata sql.NullString

	if err := row.Scan(
		&j.ID, &j.MessageID, &j.PlayerID, &j.PlayerName,
		&j.Payload, &j.ScratchpadPath, &status,
		&approvalMetadata, &createdAt, &completedAt,
	); err != nil {
		return nil, fmt.Errorf("scan job: %w", err)
	}
	j.Status = JobStatus(status)
	j.CreatedAt = parseTime(createdAt)
	j.CompletedAt = nullTime(&completedAt)
	if approvalMetadata.Valid {
		j.ApprovalMetadata = &approvalMetadata.String
	}
	return &j, nil
}

func scanJobs(rows *sql.Rows) ([]*Job, error) {
	var jobs []*Job
	for rows.Next() {
		var j Job
		var status string
		var createdAt string
		var completedAt sql.NullTime
		var approvalMetadata sql.NullString

		if err := rows.Scan(
			&j.ID, &j.MessageID, &j.PlayerID, &j.PlayerName,
			&j.Payload, &j.ScratchpadPath, &status,
			&approvalMetadata, &createdAt, &completedAt,
		); err != nil {
			return nil, fmt.Errorf("scan job row: %w", err)
		}
		j.Status = JobStatus(status)
		j.CreatedAt = parseTime(createdAt)
		j.CompletedAt = nullTime(&completedAt)
		if approvalMetadata.Valid {
			j.ApprovalMetadata = &approvalMetadata.String
		}
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
}
