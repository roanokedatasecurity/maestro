package store

import (
	"encoding/json"
	"testing"
)

// ─── AssignmentPayload tests ───────────────────────────────────────────────────

// TestNewAssignmentPayload_NilParams verifies nil params is converted to empty map.
func TestNewAssignmentPayload_NilParams(t *testing.T) {
	ap := NewAssignmentPayload("do the thing", nil)
	if ap.Task != "do the thing" {
		t.Errorf("Task: want %q got %q", "do the thing", ap.Task)
	}
	if ap.Params == nil {
		t.Error("Params should not be nil when passed nil")
	}
	if len(ap.Params) != 0 {
		t.Errorf("Params should be empty, got %v", ap.Params)
	}
}

// TestNewAssignmentPayload_WithParams verifies params are preserved.
func TestNewAssignmentPayload_WithParams(t *testing.T) {
	params := map[string]any{"depth": "standard", "target": "Netflix"}
	ap := NewAssignmentPayload("research", params)
	if ap.Params["depth"] != "standard" {
		t.Errorf("depth: want standard got %v", ap.Params["depth"])
	}
	if ap.Params["target"] != "Netflix" {
		t.Errorf("target: want Netflix got %v", ap.Params["target"])
	}
}

// TestParseAssignmentPayload_ValidJSON verifies a properly-formed JSON round-trip.
func TestParseAssignmentPayload_ValidJSON(t *testing.T) {
	original := NewAssignmentPayload("investigate logs", map[string]any{"account": "abc123"})
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := ParseAssignmentPayload(string(b))
	if err != nil {
		t.Fatalf("ParseAssignmentPayload: %v", err)
	}
	if got.Task != "investigate logs" {
		t.Errorf("Task: want %q got %q", "investigate logs", got.Task)
	}
	if got.Params["account"] != "abc123" {
		t.Errorf("account: want abc123 got %v", got.Params["account"])
	}
}

// TestParseAssignmentPayload_BackwardCompat verifies that a raw (non-JSON) string
// is treated as task with empty params — backward-compatible with Sprint 1 smoke data.
func TestParseAssignmentPayload_BackwardCompat(t *testing.T) {
	raw := "This is a plain text assignment from Sprint 1"
	ap, err := ParseAssignmentPayload(raw)
	if err != nil {
		t.Fatalf("ParseAssignmentPayload (raw): %v", err)
	}
	if ap.Task != raw {
		t.Errorf("Task: want %q got %q", raw, ap.Task)
	}
	if ap.Params == nil {
		t.Error("Params should not be nil for backward-compat case")
	}
	if len(ap.Params) != 0 {
		t.Errorf("Params should be empty for plain text, got %v", ap.Params)
	}
}

// TestParseAssignmentPayload_NilParamsInJSON verifies params defaults to empty map
// when the JSON object omits the params field.
func TestParseAssignmentPayload_NilParamsInJSON(t *testing.T) {
	raw := `{"task":"do something"}`
	ap, err := ParseAssignmentPayload(raw)
	if err != nil {
		t.Fatalf("ParseAssignmentPayload: %v", err)
	}
	if ap.Task != "do something" {
		t.Errorf("Task: want %q got %q", "do something", ap.Task)
	}
	if ap.Params == nil {
		t.Error("Params should not be nil when omitted from JSON")
	}
}
