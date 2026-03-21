package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AssignmentPayload is the structured envelope for Assignment messages.
// Task is the human-readable instruction. Params carries typed per-assignment
// context that varies per job (account IDs, depth flags, etc.).
// Free-form assignments use Params: {}.
// Payload column type unchanged (string/TEXT) — semantics locked going forward.
type AssignmentPayload struct {
	Task   string         `json:"task"`
	Params map[string]any `json:"params"`
}

// NewAssignmentPayload returns an AssignmentPayload with params defaulting to
// an empty map if nil.
func NewAssignmentPayload(task string, params map[string]any) AssignmentPayload {
	if params == nil {
		params = map[string]any{}
	}
	return AssignmentPayload{Task: task, Params: params}
}

// ParseAssignmentPayload unmarshals a JSON-encoded AssignmentPayload.
// If raw is not valid JSON, the entire string is treated as the task field
// with empty params — backward-compatible with Sprint 1 free-form payloads.
func ParseAssignmentPayload(raw string) (AssignmentPayload, error) {
	var ap AssignmentPayload
	if err := json.Unmarshal([]byte(raw), &ap); err != nil {
		// Backward compat: treat entire raw string as task.
		return AssignmentPayload{Task: raw, Params: map[string]any{}}, nil
	}
	if ap.Params == nil {
		ap.Params = map[string]any{}
	}
	return ap, nil
}

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
