// Package state persists the previously-declared package sets so bigkis can
// compute correct removals on subsequent runs.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	SchemaVersion = 1
	SystemPath    = "/var/lib/bigkis/state.json"
)

// State is the on-disk schema. Per-plugin state is left as raw JSON so plugins
// can evolve their schemas independently of this package.
type State struct {
	SchemaVersion int                        `json:"schemaVersion"`
	LastApplied   map[string]json.RawMessage `json:"lastApplied"`
}

func empty() *State {
	return &State{SchemaVersion: SchemaVersion, LastApplied: map[string]json.RawMessage{}}
}

// Load reads the state file at path. A missing file returns an empty State.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return empty(), nil
		}
		return nil, fmt.Errorf("read state %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}
	if s.LastApplied == nil {
		s.LastApplied = map[string]json.RawMessage{}
	}
	return &s, nil
}

// Save writes the state atomically (write tmp + rename).
func Save(path string, s *State) error {
	if s == nil {
		s = empty()
	}
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SchemaVersion
	}
	if s.LastApplied == nil {
		s.LastApplied = map[string]json.RawMessage{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write state tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename state: %w", err)
	}
	return nil
}

// Get unmarshals plugin-specific state into v. Returns false if the plugin has
// no state yet (caller should treat as first run).
func (s *State) Get(plugin string, v any) (bool, error) {
	raw, ok := s.LastApplied[plugin]
	if !ok || len(raw) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return true, fmt.Errorf("decode %s state: %w", plugin, err)
	}
	return true, nil
}

// Set replaces plugin-specific state.
func (s *State) Set(plugin string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s state: %w", plugin, err)
	}
	if s.LastApplied == nil {
		s.LastApplied = map[string]json.RawMessage{}
	}
	s.LastApplied[plugin] = raw
	return nil
}

// DefaultPath returns the system state path when running as root, or a
// per-user fallback otherwise.
func DefaultPath() string {
	if os.Geteuid() == 0 {
		return SystemPath
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "bigkis", "state.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "bigkis", "state.json")
	}
	return SystemPath
}
