// Package catalog resolves and hydrates Maestro player catalog entries.
//
// Catalog entries are YAML files that define the identity, spawn prompt, and
// assignment parameter schema for a type of player. They live in:
//
//   ~/.maestro/players/<name>.yaml   (local, machine-specific)
//   shared/players/<name>.yaml       (org-wide, from oleria-ai-memory repo — phase 2)
//
// Resolution order: local overrides shared. A local file with the same name
// takes precedence over the shared catalog. This mirrors git config resolution
// (system → global → local; local wins).
//
// For Sprint 2 only the local path is implemented. The shared path is stubbed
// so the resolution function has the right shape without wiring the memory repo.
package catalog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/roanokedatasecurity/maestro/internal/store"
	"gopkg.in/yaml.v3"
)

// ParamDef describes one parameter in a catalog entry's params block or
// assignment_params block.
type ParamDef struct {
	Type        string   `yaml:"type"`
	Enum        []string `yaml:"enum,omitempty"`
	Default     any      `yaml:"default,omitempty"`
	Required    bool     `yaml:"required,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Items       string   `yaml:"items,omitempty"` // for type: array
}

// CatalogEntry is the parsed representation of a player catalog YAML file.
// Fields correspond directly to the schema defined in ipc-design.md §8.
type CatalogEntry struct {
	Name                  string               `yaml:"name"`
	Description           string               `yaml:"description"`
	Params                map[string]ParamDef  `yaml:"params"`
	Notes                 string               `yaml:"notes"`
	SpawnPromptTemplate   string               `yaml:"spawn_prompt_template"`
	AssignmentParams      map[string]ParamDef  `yaml:"assignment_params"`
}

// ResolveCatalogEntry looks up a catalog entry by name.
//
// Resolution order:
//  1. ~/.maestro/players/<name>.yaml  (local override — wins if present)
//  2. shared/players/<name>.yaml      (org-wide default — stub; not yet wired)
//
// Returns the raw YAML bytes so the caller can unmarshal or log the source.
// Returns an error if the entry is not found in either location.
func ResolveCatalogEntry(name string) ([]byte, error) {
	localPath, err := localCatalogPath(name)
	if err != nil {
		return nil, fmt.Errorf("catalog: resolve local path for %q: %w", name, err)
	}

	data, err := os.ReadFile(localPath)
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("catalog: read local entry %q: %w", localPath, err)
	}

	// Stub: shared catalog path (org-wide, memory repo) — not yet implemented.
	// When MAESTRO-B-26 lands, fall through to the cloned shared/players/ path here.
	return nil, fmt.Errorf("catalog: entry %q not found (checked %s; shared catalog not yet wired)", name, localPath)
}

// ParseCatalogEntry unmarshals raw YAML bytes into a CatalogEntry.
func ParseCatalogEntry(data []byte) (*CatalogEntry, error) {
	var entry CatalogEntry
	if err := yaml.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("catalog: parse entry: %w", err)
	}
	return &entry, nil
}

// HydrateSpawnPrompt merges profile.Params with catalog param defaults and
// executes the entry's spawn_prompt_template, returning the final prompt string.
//
// Template data is a map[string]any built by:
//  1. Starting with catalog param defaults (from entry.Params[k].Default)
//  2. Overlaying profile.Params (per-session overrides win)
//  3. Adding .notes from profile.Notes (falls back to entry.Notes if empty)
//
// The template has access to a "join" function for rendering string slices:
//
//	{{join .tools ", "}}
func HydrateSpawnPrompt(entry *CatalogEntry, profile store.PlayerProfile) (string, error) {
	data := make(map[string]any)

	// Seed with catalog defaults.
	for k, def := range entry.Params {
		if def.Default != nil {
			data[k] = def.Default
		}
	}

	// Overlay per-session params.
	for k, v := range profile.Params {
		data[k] = v
	}

	// Notes: profile override wins; fall back to catalog notes.
	notes := entry.Notes
	if profile.Notes != "" {
		notes = profile.Notes
	}
	data["notes"] = notes

	// Validate required params before executing the template. Without this,
	// text/template silently renders missing keys as empty strings (missingkey=zero
	// default), producing a malformed spawn prompt with no error.
	for k, def := range entry.Params {
		if def.Required {
			if _, ok := data[k]; !ok {
				return "", fmt.Errorf("catalog: %q: required param %q not provided", entry.Name, k)
			}
		}
	}

	funcMap := template.FuncMap{
		"join": func(items any, sep string) string {
			switch v := items.(type) {
			case []string:
				return strings.Join(v, sep)
			case []any:
				strs := make([]string, len(v))
				for i, item := range v {
					strs[i] = fmt.Sprintf("%v", item)
				}
				return strings.Join(strs, sep)
			default:
				return fmt.Sprintf("%v", v)
			}
		},
	}

	tmpl, err := template.New("spawn").Funcs(funcMap).Parse(entry.SpawnPromptTemplate)
	if err != nil {
		return "", fmt.Errorf("catalog: parse spawn_prompt_template for %q: %w", entry.Name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("catalog: execute spawn_prompt_template for %q: %w", entry.Name, err)
	}
	return buf.String(), nil
}

// localCatalogPath returns the full path to the local catalog entry YAML file.
func localCatalogPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".maestro", "players", name+".yaml"), nil
}
