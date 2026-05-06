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

// Save writes the state atomically and durably (write tmp, fsync the file,
// rename, fsync the parent dir). On crash or power loss, the file at path is
// either the previous state or the new state, never a partial write.
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
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return atomicWrite(path, data, 0o644)
}

// atomicWrite writes data to path atomically and durably. It writes to a
// sibling .tmp, fsyncs the file, renames over path, then fsyncs the parent
// directory so the rename itself is durable.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	// Best-effort fsync of the parent dir so the rename survives a crash.
	// Some filesystems / platforms don't permit opening dirs for sync; ignore.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// AtomicWrite writes data to path atomically and durably (tmp + fsync +
// rename + dir fsync). Exposed so other packages (lockfile, rollback) can
// share the same durability semantics.
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	return atomicWrite(path, data, mode)
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
