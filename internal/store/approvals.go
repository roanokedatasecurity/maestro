package store

import (
	"database/sql"
	"fmt"
	"time"
)

type ApprovalDecision string

const (
	ApprovalDecisionAutonomous ApprovalDecision = "Autonomous"
	ApprovalDecisionHuman      ApprovalDecision = "Human"
)

type Approval struct {
	ID        string
	JobID     string
	MessageID string
	Scorecard string // JSON blob
	Decision  *ApprovalDecision
	DecidedAt *time.Time
	CreatedAt time.Time
}

// CreateApproval records a pending approval request for a job. Scorecard is a
// JSON string — pass "{}" if no scorecard data is available yet.
func (s *Store) CreateApproval(jobID, messageID, scorecard string) (*Approval, error) {
	id := newID()
	_, err := s.db.Exec(`
		INSERT INTO approvals (id, job_id, message_id, scorecard)
		VALUES (?, ?, ?, ?)`,
		id, jobID, messageID, scorecard,
	)
	if err != nil {
		return nil, fmt.Errorf("create approval: %w", err)
	}
	return s.GetApproval(id)
}

// GetApproval returns an approval by ID.
func (s *Store) GetApproval(id string) (*Approval, error) {
	row := s.db.QueryRow(`
		SELECT id, job_id, message_id, scorecard, decision, decided_at, created_at
		FROM approvals WHERE id = ?`, id)
	return scanApproval(row)
}

// RecordDecision stamps a decision on the given approval.
func (s *Store) RecordDecision(id string, decision ApprovalDecision) error {
	res, err := s.db.Exec(`
		UPDATE approvals SET decision = ?, decided_at = datetime('now') WHERE id = ?`,
		string(decision), id,
	)
	if err != nil {
		return fmt.Errorf("record decision: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("record decision: approval %q not found", id)
	}
	return nil
}

// ListPendingApprovals returns all approvals with no decision yet, oldest first.
func (s *Store) ListPendingApprovals() ([]*Approval, error) {
	rows, err := s.db.Query(`
		SELECT id, job_id, message_id, scorecard, decision, decided_at, created_at
		FROM approvals WHERE decision IS NULL ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending approvals: %w", err)
	}
	defer rows.Close()

	var approvals []*Approval
	for rows.Next() {
		a, err := scanApprovalRow(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, a)
	}
	return approvals, rows.Err()
}

func scanApproval(row *sql.Row) (*Approval, error) {
	var a Approval
	var decision sql.NullString
	var decidedAt sql.NullTime
	var createdAt string

	if err := row.Scan(
		&a.ID, &a.JobID, &a.MessageID, &a.Scorecard,
		&decision, &decidedAt, &createdAt,
	); err != nil {
		return nil, fmt.Errorf("scan approval: %w", err)
	}
	if decision.Valid {
		d := ApprovalDecision(decision.String)
		a.Decision = &d
	}
	a.DecidedAt = nullTime(&decidedAt)
	a.CreatedAt = parseTime(createdAt)
	return &a, nil
}

func scanApprovalRow(rows *sql.Rows) (*Approval, error) {
	var a Approval
	var decision sql.NullString
	var decidedAt sql.NullTime
	var createdAt string

	if err := rows.Scan(
		&a.ID, &a.JobID, &a.MessageID, &a.Scorecard,
		&decision, &decidedAt, &createdAt,
	); err != nil {
		return nil, fmt.Errorf("scan approval row: %w", err)
	}
	if decision.Valid {
		d := ApprovalDecision(decision.String)
		a.Decision = &d
	}
	a.DecidedAt = nullTime(&decidedAt)
	a.CreatedAt = parseTime(createdAt)
	return &a, nil
}
