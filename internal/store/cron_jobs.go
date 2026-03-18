package store

import (
	"database/sql"
	"fmt"
	"time"
)

// CronJob represents a scheduled recurring task.
type CronJob struct {
	ID             string
	Name           string
	ScriptPath     string
	Schedule       string
	ScratchpadPath string
	OwnerPlayerID  *string
	LastFiredAt    *time.Time
	NextFireAt     *time.Time
	CreatedAt      time.Time
}

// CreateCronJob inserts a new cron job record. The ID must be set by the caller.
func (s *Store) CreateCronJob(cj CronJob) error {
	_, err := s.db.Exec(`
		INSERT INTO cron_jobs (id, name, script_path, schedule, scratchpad_path, owner_player_id)
		VALUES (?, ?, ?, ?, ?, ?)`,
		cj.ID, cj.Name, cj.ScriptPath, cj.Schedule, cj.ScratchpadPath, cj.OwnerPlayerID,
	)
	if err != nil {
		return fmt.Errorf("create cron job: %w", err)
	}
	return nil
}

// GetCronJob returns a cron job by ID.
func (s *Store) GetCronJob(id string) (*CronJob, error) {
	row := s.db.QueryRow(`
		SELECT id, name, script_path, schedule, scratchpad_path,
		       owner_player_id, last_fired_at, next_fire_at, created_at
		FROM cron_jobs WHERE id = ?`, id)
	return scanCronJob(row)
}

// ListCronJobs returns all cron jobs ordered by creation time.
func (s *Store) ListCronJobs() ([]*CronJob, error) {
	rows, err := s.db.Query(`
		SELECT id, name, script_path, schedule, scratchpad_path,
		       owner_player_id, last_fired_at, next_fire_at, created_at
		FROM cron_jobs ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list cron jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*CronJob
	for rows.Next() {
		cj, err := scanCronJobRow(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, cj)
	}
	return jobs, rows.Err()
}

// DeleteCronJob removes a cron job by ID.
func (s *Store) DeleteCronJob(id string) error {
	res, err := s.db.Exec("DELETE FROM cron_jobs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete cron job: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("delete cron job: %q not found", id)
	}
	return nil
}

// UpdateCronJobFired records the last fire time and next scheduled fire time.
func (s *Store) UpdateCronJobFired(id string, lastFiredAt time.Time, nextFireAt time.Time) error {
	res, err := s.db.Exec(`
		UPDATE cron_jobs SET last_fired_at = ?, next_fire_at = ? WHERE id = ?`,
		lastFiredAt.UTC().Format(time.RFC3339),
		nextFireAt.UTC().Format(time.RFC3339),
		id,
	)
	if err != nil {
		return fmt.Errorf("update cron job fired: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("update cron job fired: %q not found", id)
	}
	return nil
}

func scanCronJob(row *sql.Row) (*CronJob, error) {
	var cj CronJob
	var ownerPlayerID sql.NullString
	var lastFiredAt, nextFireAt sql.NullTime
	var createdAt string

	if err := row.Scan(
		&cj.ID, &cj.Name, &cj.ScriptPath, &cj.Schedule, &cj.ScratchpadPath,
		&ownerPlayerID, &lastFiredAt, &nextFireAt, &createdAt,
	); err != nil {
		return nil, fmt.Errorf("scan cron job: %w", err)
	}
	if ownerPlayerID.Valid {
		cj.OwnerPlayerID = &ownerPlayerID.String
	}
	cj.LastFiredAt = nullTime(&lastFiredAt)
	cj.NextFireAt = nullTime(&nextFireAt)
	cj.CreatedAt = parseTime(createdAt)
	return &cj, nil
}

func scanCronJobRow(rows *sql.Rows) (*CronJob, error) {
	var cj CronJob
	var ownerPlayerID sql.NullString
	var lastFiredAt, nextFireAt sql.NullTime
	var createdAt string

	if err := rows.Scan(
		&cj.ID, &cj.Name, &cj.ScriptPath, &cj.Schedule, &cj.ScratchpadPath,
		&ownerPlayerID, &lastFiredAt, &nextFireAt, &createdAt,
	); err != nil {
		return nil, fmt.Errorf("scan cron job row: %w", err)
	}
	if ownerPlayerID.Valid {
		cj.OwnerPlayerID = &ownerPlayerID.String
	}
	cj.LastFiredAt = nullTime(&lastFiredAt)
	cj.NextFireAt = nullTime(&nextFireAt)
	cj.CreatedAt = parseTime(createdAt)
	return &cj, nil
}
