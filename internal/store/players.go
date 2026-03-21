package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type PlayerStatus string

const (
	PlayerStatusIdle    PlayerStatus = "Idle"
	PlayerStatusRunning PlayerStatus = "Running"
	PlayerStatusDead    PlayerStatus = "Dead"
)

// PlayerProfile is the optional spawn profile attached to a player at
// registration time. It links the player to a catalog entry and carries
// per-session overrides for catalog params and notes.
// Stored as a JSON string in the profile column; NULL if no profile was provided.
type PlayerProfile struct {
	CatalogEntry string         `json:"catalog_entry,omitempty"` // name of YAML file in catalog
	Params       map[string]any `json:"params,omitempty"`        // merged with catalog param_schema defaults
	Notes        string         `json:"notes,omitempty"`         // per-session freeform override
}

type Player struct {
	ID          string
	Name        string
	Status      PlayerStatus
	IsConductor bool
	Profile     *PlayerProfile // nil if no profile was provided at registration
	CreatedAt   time.Time
	LastSeenAt  time.Time
}

// CreatePlayer registers a new player. If isConductor is true and a Conductor
// already exists, an error is returned — there can only be one.
// profile is optional — pass nil for players registered without a spawn profile.
func (s *Store) CreatePlayer(name string, isConductor bool, profile *PlayerProfile) (*Player, error) {
	if isConductor {
		var count int
		if err := s.db.QueryRow(
			"SELECT COUNT(*) FROM players WHERE is_conductor = 1 AND status != 'Dead'",
		).Scan(&count); err != nil {
			return nil, fmt.Errorf("check conductor uniqueness: %w", err)
		}
		if count > 0 {
			return nil, fmt.Errorf("create player: a live Conductor already exists")
		}
	}

	id := newID()
	conductorInt := 0
	if isConductor {
		conductorInt = 1
	}

	var profileJSON *string
	if profile != nil {
		b, err := json.Marshal(profile)
		if err != nil {
			return nil, fmt.Errorf("create player: marshal profile: %w", err)
		}
		jsonStr := string(b)
		profileJSON = &jsonStr
	}

	_, err := s.db.Exec(`
		INSERT INTO players (id, name, is_conductor, profile) VALUES (?, ?, ?, ?)`,
		id, name, conductorInt, profileJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("create player: %w", err)
	}
	return s.GetPlayer(id)
}

// GetPlayer returns a player by ID.
func (s *Store) GetPlayer(id string) (*Player, error) {
	row := s.db.QueryRow(`
		SELECT id, name, status, is_conductor, profile, created_at, last_seen_at
		FROM players WHERE id = ?`, id)
	return scanPlayer(row)
}

// GetPlayerByName returns the most recently created player with the given name.
func (s *Store) GetPlayerByName(name string) (*Player, error) {
	row := s.db.QueryRow(`
		SELECT id, name, status, is_conductor, profile, created_at, last_seen_at
		FROM players WHERE name = ? ORDER BY created_at DESC LIMIT 1`, name)
	return scanPlayer(row)
}

// UpdatePlayerStatus transitions a player to the given status.
func (s *Store) UpdatePlayerStatus(id string, status PlayerStatus) error {
	res, err := s.db.Exec(
		"UPDATE players SET status = ? WHERE id = ?", string(status), id,
	)
	if err != nil {
		return fmt.Errorf("update player status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("update player status: player %q not found", id)
	}
	return nil
}

// UpdateLastSeen sets last_seen_at to now for the given player.
func (s *Store) UpdateLastSeen(id string) error {
	res, err := s.db.Exec(
		"UPDATE players SET last_seen_at = datetime('now') WHERE id = ?", id,
	)
	if err != nil {
		return fmt.Errorf("update last seen: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("update last seen: player %q not found", id)
	}
	return nil
}

// ListPlayers returns all players ordered by created_at ascending.
func (s *Store) ListPlayers() ([]*Player, error) {
	rows, err := s.db.Query(`
		SELECT id, name, status, is_conductor, profile, created_at, last_seen_at
		FROM players ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list players: %w", err)
	}
	defer rows.Close()

	var players []*Player
	for rows.Next() {
		p, err := scanPlayerRow(rows)
		if err != nil {
			return nil, err
		}
		players = append(players, p)
	}
	return players, rows.Err()
}

func scanPlayer(row *sql.Row) (*Player, error) {
	var p Player
	var conductorInt int
	var createdAt, lastSeenAt string
	var status string
	var profileJSON sql.NullString

	if err := row.Scan(
		&p.ID, &p.Name, &status, &conductorInt, &profileJSON, &createdAt, &lastSeenAt,
	); err != nil {
		return nil, fmt.Errorf("scan player: %w", err)
	}
	p.Status = PlayerStatus(status)
	p.IsConductor = conductorInt == 1
	p.CreatedAt = parseTime(createdAt)
	p.LastSeenAt = parseTime(lastSeenAt)
	if profileJSON.Valid && profileJSON.String != "" {
		var profile PlayerProfile
		if err := json.Unmarshal([]byte(profileJSON.String), &profile); err != nil {
			fmt.Fprintf(os.Stderr, "store: player %s: unmarshal profile: %v\n", p.ID, err)
		} else {
			p.Profile = &profile
		}
	}
	return &p, nil
}

func scanPlayerRow(rows *sql.Rows) (*Player, error) {
	var p Player
	var conductorInt int
	var createdAt, lastSeenAt string
	var status string
	var profileJSON sql.NullString

	if err := rows.Scan(
		&p.ID, &p.Name, &status, &conductorInt, &profileJSON, &createdAt, &lastSeenAt,
	); err != nil {
		return nil, fmt.Errorf("scan player row: %w", err)
	}
	p.Status = PlayerStatus(status)
	p.IsConductor = conductorInt == 1
	p.CreatedAt = parseTime(createdAt)
	p.LastSeenAt = parseTime(lastSeenAt)
	if profileJSON.Valid && profileJSON.String != "" {
		var profile PlayerProfile
		if err := json.Unmarshal([]byte(profileJSON.String), &profile); err != nil {
			fmt.Fprintf(os.Stderr, "store: player %s: unmarshal profile: %v\n", p.ID, err)
		} else {
			p.Profile = &profile
		}
	}
	return &p, nil
}
