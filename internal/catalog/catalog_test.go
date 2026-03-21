package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roanokedatasecurity/maestro/internal/store"
)

// researcherYAML is a minimal researcher catalog entry for testing.
// It matches the schema in docs/players/researcher.yaml.
const researcherYAML = `
name: researcher
description: Deep research agent

params:
  domain:
    type: string
    enum: [customer, competitor, codebase, general]
    required: true
  output_format:
    type: string
    default: bullet-summary
  tools:
    type: array
    default: [web, slack, gdrive]

notes: |
  Always cite sources.

spawn_prompt_template: |
  You are a Researcher player.
  Domain: {{.domain}}
  Output format: {{.output_format}}
  Authorized tools: {{join .tools ", "}}
  {{.notes}}
  Wait for your first assignment.
`

// writeTestEntry writes a YAML catalog entry to dir/<name>.yaml and returns the path.
func writeTestEntry(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write test catalog entry: %v", err)
	}
	return path
}

// ─── ResolveCatalogEntry tests ─────────────────────────────────────────────────

// TestResolveCatalogEntry_LocalFound verifies resolution succeeds when the local
// YAML file exists. We temporarily override localCatalogPath by placing a file
// in a temp dir and patching the home dir via t.Setenv.
func TestResolveCatalogEntry_LocalFound(t *testing.T) {
	// Override HOME so localCatalogPath points at our temp dir.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create ~/.maestro/players/ directory.
	playersDir := filepath.Join(tmpHome, ".maestro", "players")
	if err := os.MkdirAll(playersDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestEntry(t, playersDir, "researcher", researcherYAML)

	data, err := ResolveCatalogEntry("researcher")
	if err != nil {
		t.Fatalf("ResolveCatalogEntry: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty YAML bytes")
	}
}

// TestResolveCatalogEntry_NotFound verifies an error is returned when the entry
// is absent from both local and shared catalogs.
func TestResolveCatalogEntry_NotFound(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create the directory but add no entries.
	if err := os.MkdirAll(filepath.Join(tmpHome, ".maestro", "players"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := ResolveCatalogEntry("nonexistent-player")
	if err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
}

// ─── HydrateSpawnPrompt tests ──────────────────────────────────────────────────

// TestHydrateSpawnPrompt_ResearcherDefaults verifies that catalog defaults are
// applied when no profile params override them.
func TestHydrateSpawnPrompt_ResearcherDefaults(t *testing.T) {
	entry, err := ParseCatalogEntry([]byte(researcherYAML))
	if err != nil {
		t.Fatalf("ParseCatalogEntry: %v", err)
	}

	profile := store.PlayerProfile{
		CatalogEntry: "researcher",
		Params:       map[string]any{"domain": "customer"},
	}

	prompt, err := HydrateSpawnPrompt(entry, profile)
	if err != nil {
		t.Fatalf("HydrateSpawnPrompt: %v", err)
	}

	if !strings.Contains(prompt, "Domain: customer") {
		t.Errorf("prompt missing 'Domain: customer': %q", prompt)
	}
	// Default output_format should be applied.
	if !strings.Contains(prompt, "Output format: bullet-summary") {
		t.Errorf("prompt missing default output_format: %q", prompt)
	}
	// Default tools should be rendered via join.
	if !strings.Contains(prompt, "web, slack, gdrive") {
		t.Errorf("prompt missing default tools: %q", prompt)
	}
}

// TestHydrateSpawnPrompt_ProfileOverridesDefaults verifies per-session params
// override catalog defaults.
func TestHydrateSpawnPrompt_ProfileOverridesDefaults(t *testing.T) {
	entry, err := ParseCatalogEntry([]byte(researcherYAML))
	if err != nil {
		t.Fatalf("ParseCatalogEntry: %v", err)
	}

	profile := store.PlayerProfile{
		CatalogEntry: "researcher",
		Params: map[string]any{
			"domain":        "competitor",
			"output_format": "narrative",
		},
	}

	prompt, err := HydrateSpawnPrompt(entry, profile)
	if err != nil {
		t.Fatalf("HydrateSpawnPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Domain: competitor") {
		t.Errorf("expected Domain: competitor in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Output format: narrative") {
		t.Errorf("expected Output format: narrative in prompt: %q", prompt)
	}
}

// TestHydrateSpawnPrompt_ProfileNotesOverride verifies profile.Notes overrides
// catalog-level notes.
func TestHydrateSpawnPrompt_ProfileNotesOverride(t *testing.T) {
	entry, err := ParseCatalogEntry([]byte(researcherYAML))
	if err != nil {
		t.Fatalf("ParseCatalogEntry: %v", err)
	}

	profile := store.PlayerProfile{
		CatalogEntry: "researcher",
		Params:       map[string]any{"domain": "codebase"},
		Notes:        "Focus on internal packages only.",
	}

	prompt, err := HydrateSpawnPrompt(entry, profile)
	if err != nil {
		t.Fatalf("HydrateSpawnPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Focus on internal packages only.") {
		t.Errorf("expected profile notes in prompt: %q", prompt)
	}
	// Original catalog notes should not appear.
	if strings.Contains(prompt, "Always cite sources.") {
		t.Errorf("catalog notes should be overridden by profile notes: %q", prompt)
	}
}

// TestHydrateSpawnPrompt_CatalogNotesFallback verifies catalog notes are used when
// profile.Notes is empty.
func TestHydrateSpawnPrompt_CatalogNotesFallback(t *testing.T) {
	entry, err := ParseCatalogEntry([]byte(researcherYAML))
	if err != nil {
		t.Fatalf("ParseCatalogEntry: %v", err)
	}

	profile := store.PlayerProfile{
		CatalogEntry: "researcher",
		Params:       map[string]any{"domain": "general"},
		Notes:        "", // empty → fall back to catalog notes
	}

	prompt, err := HydrateSpawnPrompt(entry, profile)
	if err != nil {
		t.Fatalf("HydrateSpawnPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Always cite sources.") {
		t.Errorf("expected catalog notes in prompt: %q", prompt)
	}
}

// TestResolveCatalogEntry_ReadError verifies an error is returned when the local
// path exists but is unreadable (a directory at the path acts as a stand-in).
func TestResolveCatalogEntry_ReadError(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create a directory where the YAML file would be — reading a directory
	// as a file returns an error that is NOT os.IsNotExist.
	badPath := filepath.Join(tmpHome, ".maestro", "players", "badentry.yaml")
	if err := os.MkdirAll(badPath, 0700); err != nil {
		t.Fatalf("mkdir as fake entry: %v", err)
	}

	_, err := ResolveCatalogEntry("badentry")
	if err == nil {
		t.Fatal("expected error reading directory as YAML file, got nil")
	}
}

// TestParseCatalogEntry_InvalidYAML verifies an error is returned for malformed YAML.
func TestParseCatalogEntry_InvalidYAML(t *testing.T) {
	bad := []byte("name: [unclosed")
	_, err := ParseCatalogEntry(bad)
	if err == nil {
		t.Fatal("expected parse error for invalid YAML, got nil")
	}
}

// TestHydrateSpawnPrompt_InvalidTemplate verifies an error is returned when the
// spawn_prompt_template contains a syntax error.
func TestHydrateSpawnPrompt_InvalidTemplate(t *testing.T) {
	entry := &CatalogEntry{
		Name:                "broken",
		SpawnPromptTemplate: "{{.domain} unclosed brace",
	}
	profile := store.PlayerProfile{Params: map[string]any{"domain": "test"}}
	_, err := HydrateSpawnPrompt(entry, profile)
	if err == nil {
		t.Fatal("expected error for invalid template, got nil")
	}
}

// TestHydrateSpawnPrompt_JoinWithStringSlice verifies the join template func
// works with a []string value (not []any).
func TestHydrateSpawnPrompt_JoinWithStringSlice(t *testing.T) {
	entry, err := ParseCatalogEntry([]byte(researcherYAML))
	if err != nil {
		t.Fatalf("ParseCatalogEntry: %v", err)
	}
	profile := store.PlayerProfile{
		Params: map[string]any{
			"domain": "codebase",
			"tools":  []string{"web", "gdrive"},
		},
	}
	prompt, err := HydrateSpawnPrompt(entry, profile)
	if err != nil {
		t.Fatalf("HydrateSpawnPrompt: %v", err)
	}
	if !strings.Contains(prompt, "web, gdrive") {
		t.Errorf("expected 'web, gdrive' in prompt: %q", prompt)
	}
}

// TestParseCatalogEntry_RequiredFields verifies the YAML parser captures all
// top-level fields from the researcher reference entry.
func TestParseCatalogEntry_RequiredFields(t *testing.T) {
	entry, err := ParseCatalogEntry([]byte(researcherYAML))
	if err != nil {
		t.Fatalf("ParseCatalogEntry: %v", err)
	}
	if entry.Name != "researcher" {
		t.Errorf("Name: want researcher got %q", entry.Name)
	}
	if entry.SpawnPromptTemplate == "" {
		t.Error("SpawnPromptTemplate should not be empty")
	}
	if _, ok := entry.Params["domain"]; !ok {
		t.Error("Params should include 'domain'")
	}
	if entry.Params["domain"].Required != true {
		t.Error("domain param should be required")
	}
}
