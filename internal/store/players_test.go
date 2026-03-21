package store

import (
	"testing"
)

// ─── PlayerProfile tests ───────────────────────────────────────────────────────

// TestCreatePlayer_WithProfile verifies profile is persisted and retrieved.
func TestCreatePlayer_WithProfile(t *testing.T) {
	s := openTestStore(t)

	profile := &PlayerProfile{
		CatalogEntry: "researcher",
		Params:       map[string]any{"domain": "customer", "output_format": "bullet-summary"},
		Notes:        "Focus on Netflix. Cite Salesforce first.",
	}

	p, err := s.CreatePlayer("researcher-1", false, profile)
	if err != nil {
		t.Fatalf("CreatePlayer: %v", err)
	}

	if p.Profile == nil {
		t.Fatal("Profile should not be nil after CreatePlayer with profile")
	}
	if p.Profile.CatalogEntry != "researcher" {
		t.Errorf("CatalogEntry: want researcher got %q", p.Profile.CatalogEntry)
	}
	if p.Profile.Params["domain"] != "customer" {
		t.Errorf("Params[domain]: want customer got %v", p.Profile.Params["domain"])
	}
	if p.Profile.Notes != "Focus on Netflix. Cite Salesforce first." {
		t.Errorf("Notes mismatch: got %q", p.Profile.Notes)
	}
}

// TestCreatePlayer_NilProfile verifies nil profile results in nil Profile on Player.
func TestCreatePlayer_NilProfile(t *testing.T) {
	s := openTestStore(t)

	p, err := s.CreatePlayer("coder-1", false, nil)
	if err != nil {
		t.Fatalf("CreatePlayer (nil profile): %v", err)
	}
	if p.Profile != nil {
		t.Errorf("Profile should be nil for player created without profile, got %+v", p.Profile)
	}
}

// TestGetPlayer_ProfileRoundTrip verifies Get returns the same profile that was stored.
func TestGetPlayer_ProfileRoundTrip(t *testing.T) {
	s := openTestStore(t)

	profile := &PlayerProfile{
		CatalogEntry: "ticket-coder",
		Params:       map[string]any{"repo": "maestro"},
	}
	created, err := s.CreatePlayer("ticket-coder-1", false, profile)
	if err != nil {
		t.Fatalf("CreatePlayer: %v", err)
	}

	got, err := s.GetPlayer(created.ID)
	if err != nil {
		t.Fatalf("GetPlayer: %v", err)
	}
	if got.Profile == nil {
		t.Fatal("GetPlayer: Profile should not be nil")
	}
	if got.Profile.CatalogEntry != "ticket-coder" {
		t.Errorf("CatalogEntry: want ticket-coder got %q", got.Profile.CatalogEntry)
	}
	if got.Profile.Params["repo"] != "maestro" {
		t.Errorf("Params[repo]: want maestro got %v", got.Profile.Params["repo"])
	}
}

// TestListPlayers_IncludesProfile verifies ListPlayers scans the profile column.
func TestListPlayers_IncludesProfile(t *testing.T) {
	s := openTestStore(t)

	_, _ = s.CreatePlayer("no-profile", false, nil)
	_, _ = s.CreatePlayer("with-profile", false, &PlayerProfile{CatalogEntry: "monitor"})

	players, err := s.ListPlayers()
	if err != nil {
		t.Fatalf("ListPlayers: %v", err)
	}
	if len(players) != 2 {
		t.Fatalf("want 2 players got %d", len(players))
	}

	// Find by name since order is by created_at.
	byName := make(map[string]*Player)
	for _, p := range players {
		byName[p.Name] = p
	}

	if byName["no-profile"].Profile != nil {
		t.Error("no-profile player should have nil Profile")
	}
	if byName["with-profile"].Profile == nil {
		t.Fatal("with-profile player should have non-nil Profile")
	}
	if byName["with-profile"].Profile.CatalogEntry != "monitor" {
		t.Errorf("CatalogEntry: want monitor got %q", byName["with-profile"].Profile.CatalogEntry)
	}
}

// TestPlayerProfile_JSONMarshal verifies PlayerProfile marshals and unmarshals cleanly
// via the CreatePlayer/GetPlayer path (which uses encoding/json internally).
func TestPlayerProfile_JSONMarshal(t *testing.T) {
	profile := PlayerProfile{
		CatalogEntry: "researcher",
		Params:       map[string]any{"domain": "competitor", "depth": "deep"},
		Notes:        "Extra context here.",
	}

	// Round-trip via CreatePlayer/GetPlayer which use json.Marshal/Unmarshal internally.
	s := openTestStore(t)
	p, err := s.CreatePlayer("marshal-test", false, &profile)
	if err != nil {
		t.Fatalf("CreatePlayer: %v", err)
	}
	if p.Profile.Notes != "Extra context here." {
		t.Errorf("Notes: want %q got %q", "Extra context here.", p.Profile.Notes)
	}
}
