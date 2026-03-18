package store

import (
	"database/sql"
	"fmt"
	"time"
)

type Notification struct {
	ID        string
	MessageID *string
	JobID     *string
	Type      string
	Summary   string
	ReadAt    *time.Time
	CreatedAt time.Time
}

// CreateNotification adds a notification to the Conductor's inbox.
// messageID and jobID are optional — pass nil if not applicable.
func (s *Store) CreateNotification(messageID, jobID *string, typ, summary string) (*Notification, error) {
	id := newID()
	_, err := s.db.Exec(`
		INSERT INTO notifications (id, message_id, job_id, type, summary)
		VALUES (?, ?, ?, ?, ?)`,
		id, messageID, jobID, typ, summary,
	)
	if err != nil {
		return nil, fmt.Errorf("create notification: %w", err)
	}
	return s.GetNotification(id)
}

// GetNotification returns a notification by ID.
func (s *Store) GetNotification(id string) (*Notification, error) {
	row := s.db.QueryRow(`
		SELECT id, message_id, job_id, type, summary, read_at, created_at
		FROM notifications WHERE id = ?`, id)
	return scanNotification(row)
}

// MarkNotificationRead stamps read_at on the given notification.
func (s *Store) MarkNotificationRead(id string) error {
	res, err := s.db.Exec(
		"UPDATE notifications SET read_at = datetime('now') WHERE id = ?", id,
	)
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("mark notification read: notification %q not found", id)
	}
	return nil
}

// ListUnreadNotifications returns all notifications with read_at IS NULL,
// ordered oldest first.
func (s *Store) ListUnreadNotifications() ([]*Notification, error) {
	rows, err := s.db.Query(`
		SELECT id, message_id, job_id, type, summary, read_at, created_at
		FROM notifications WHERE read_at IS NULL ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list unread notifications: %w", err)
	}
	defer rows.Close()

	var notifs []*Notification
	for rows.Next() {
		n, err := scanNotificationRow(rows)
		if err != nil {
			return nil, err
		}
		notifs = append(notifs, n)
	}
	return notifs, rows.Err()
}

func scanNotification(row *sql.Row) (*Notification, error) {
	var n Notification
	var messageID, jobID sql.NullString
	var readAt sql.NullTime
	var createdAt string

	if err := row.Scan(
		&n.ID, &messageID, &jobID, &n.Type, &n.Summary, &readAt, &createdAt,
	); err != nil {
		return nil, fmt.Errorf("scan notification: %w", err)
	}
	if messageID.Valid {
		n.MessageID = &messageID.String
	}
	if jobID.Valid {
		n.JobID = &jobID.String
	}
	n.ReadAt = nullTime(&readAt)
	n.CreatedAt = parseTime(createdAt)
	return &n, nil
}

func scanNotificationRow(rows *sql.Rows) (*Notification, error) {
	var n Notification
	var messageID, jobID sql.NullString
	var readAt sql.NullTime
	var createdAt string

	if err := rows.Scan(
		&n.ID, &messageID, &jobID, &n.Type, &n.Summary, &readAt, &createdAt,
	); err != nil {
		return nil, fmt.Errorf("scan notification row: %w", err)
	}
	if messageID.Valid {
		n.MessageID = &messageID.String
	}
	if jobID.Valid {
		n.JobID = &jobID.String
	}
	n.ReadAt = nullTime(&readAt)
	n.CreatedAt = parseTime(createdAt)
	return &n, nil
}
