package store

import (
	"database/sql"
	"fmt"
	"time"
)

type MessageType string

const (
	MessageTypeAssignment MessageType = "Assignment"
	MessageTypeDone       MessageType = "Done"
	MessageTypeBlocked    MessageType = "Blocked"
	MessageTypeBackground MessageType = "Background"
	MessageTypeLifecycle  MessageType = "Lifecycle"
)

type Priority string

const (
	PriorityHigh   Priority = "High"
	PriorityNormal Priority = "Normal"
)

type Message struct {
	ID          string
	FromPlayer  string
	ToPlayer    string
	Type        MessageType
	Priority    Priority
	Payload     string
	WaitForAck  bool
	CreatedAt   time.Time
	DeliveredAt *time.Time
}

// CreateMessage inserts a new message and returns it with its generated ID.
func (s *Store) CreateMessage(from, to string, typ MessageType, priority Priority, payload string, waitForAck bool) (*Message, error) {
	id := newID()
	waitInt := 0
	if waitForAck {
		waitInt = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO messages (id, from_player, to_player, type, priority, payload, wait_for_ack)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, from, to, string(typ), string(priority), payload, waitInt,
	)
	if err != nil {
		return nil, fmt.Errorf("create message: %w", err)
	}
	return s.GetMessage(id)
}

// GetMessage returns a message by ID.
func (s *Store) GetMessage(id string) (*Message, error) {
	row := s.db.QueryRow(`
		SELECT id, from_player, to_player, type, priority, payload, wait_for_ack,
		       created_at, delivered_at
		FROM messages WHERE id = ?`, id)
	return scanMessage(row)
}

// MarkDelivered sets delivered_at to now for the given message.
func (s *Store) MarkDelivered(id string) error {
	res, err := s.db.Exec(
		"UPDATE messages SET delivered_at = datetime('now') WHERE id = ?", id,
	)
	if err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("mark delivered: message %q not found", id)
	}
	return nil
}

// ListUndelivered returns all undelivered messages addressed to toPlayer,
// ordered by priority (High first) then created_at ascending.
func (s *Store) ListUndelivered(toPlayer string) ([]*Message, error) {
	rows, err := s.db.Query(`
		SELECT id, from_player, to_player, type, priority, payload, wait_for_ack,
		       created_at, delivered_at
		FROM messages
		WHERE to_player = ? AND delivered_at IS NULL
		ORDER BY CASE priority WHEN 'High' THEN 0 ELSE 1 END, created_at ASC`,
		toPlayer,
	)
	if err != nil {
		return nil, fmt.Errorf("list undelivered: %w", err)
	}
	defer rows.Close()
	return scanMessages(rows)
}

func scanMessage(row *sql.Row) (*Message, error) {
	var m Message
	var waitInt int
	var createdAt string
	var deliveredAt sql.NullTime
	var typ, priority string

	if err := row.Scan(
		&m.ID, &m.FromPlayer, &m.ToPlayer, &typ, &priority,
		&m.Payload, &waitInt, &createdAt, &deliveredAt,
	); err != nil {
		return nil, fmt.Errorf("scan message: %w", err)
	}
	m.Type = MessageType(typ)
	m.Priority = Priority(priority)
	m.WaitForAck = waitInt == 1
	m.CreatedAt = parseTime(createdAt)
	m.DeliveredAt = nullTime(&deliveredAt)
	return &m, nil
}

func scanMessages(rows *sql.Rows) ([]*Message, error) {
	var msgs []*Message
	for rows.Next() {
		var m Message
		var waitInt int
		var createdAt string
		var deliveredAt sql.NullTime
		var typ, priority string

		if err := rows.Scan(
			&m.ID, &m.FromPlayer, &m.ToPlayer, &typ, &priority,
			&m.Payload, &waitInt, &createdAt, &deliveredAt,
		); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}
		m.Type = MessageType(typ)
		m.Priority = Priority(priority)
		m.WaitForAck = waitInt == 1
		m.CreatedAt = parseTime(createdAt)
		m.DeliveredAt = nullTime(&deliveredAt)
		msgs = append(msgs, &m)
	}
	return msgs, rows.Err()
}
