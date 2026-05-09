package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s == nil || s.SchemaVersion != SchemaVersion {
		t.Errorf("expected empty state with schema version %d, got %+v", SchemaVersion, s)
	}
	if s.LastApplied == nil {
		t.Error("expected non-nil LastApplied map")
	}
}

func TestLoad_InvalidJSONReturnsParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)

	if err == nil || !strings.Contains(err.Error(), "parse state") {
		t.Fatalf("expected parse state error, got %v", err)
	}
}

func TestLoad_NilLastAppliedBecomesMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load(path)

	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.LastApplied == nil {
		t.Fatal("LastApplied is nil")
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	s := &State{}
	if err := s.Set("pacman", []string{"git", "neovim"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	type flatpakState struct {
		System []string            `json:"system"`
		Users  map[string][]string `json:"users"`
	}
	if err := s.Set("flatpak", flatpakState{
		System: []string{"org.mozilla.firefox"},
		Users:  map[string][]string{"georgep": {"com.valvesoftware.Steam"}},
	}); err != nil {
		t.Fatalf("Set flatpak: %v", err)
	}

	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var pacPkgs []string
	found, err := loaded.Get("pacman", &pacPkgs)
	if err != nil {
		t.Fatalf("Get pacman: %v", err)
	}
	if !found {
		t.Fatal("pacman state not found")
	}
	if !reflect.DeepEqual(pacPkgs, []string{"git", "neovim"}) {
		t.Errorf("pacman state = %v", pacPkgs)
	}

	var fp flatpakState
	if _, err := loaded.Get("flatpak", &fp); err != nil {
		t.Fatalf("Get flatpak: %v", err)
	}
	if !reflect.DeepEqual(fp.System, []string{"org.mozilla.firefox"}) {
		t.Errorf("flatpak.system = %v", fp.System)
	}
}

func TestSave_NilStateWritesEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	if err := Save(path, nil); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.SchemaVersion != SchemaVersion || loaded.LastApplied == nil {
		t.Fatalf("loaded state = %+v", loaded)
	}
}

func TestGet_InvalidPluginJSONReturnsDecodeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"lastApplied":{"pacman":{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("invalid top-level JSON should fail before Get")
	}

	s := &State{LastApplied: map[string]json.RawMessage{"pacman": []byte("{")}}
	var v []string
	found, err := s.Get("pacman", &v)
	if !found || err == nil || !strings.Contains(err.Error(), "decode pacman state") {
		t.Fatalf("found=%v err=%v, want decode error", found, err)
	}
}

func TestGet_MissingPluginReturnsFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, &State{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var v []string
	found, err := loaded.Get("does-not-exist", &v)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Error("found should be false for missing plugin")
	}
}

func TestSet_OverwritesExisting(t *testing.T) {
	s := &State{}
	if err := s.Set("pacman", []string{"git"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("pacman", []string{"neovim", "wget"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var v []string
	if _, err := s.Get("pacman", &v); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(v, []string{"neovim", "wget"}) {
		t.Errorf("expected overwrite, got %v", v)
	}
}

func TestSave_IsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := &State{}
	if err := s.Set("pacman", []string{"git"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Overwrite with different content; ensure no .tmp leftover and content
	// reflects the latest write.
	if err := s.Set("pacman", []string{"neovim"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Save(path, s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var v []string
	if _, err := loaded.Get("pacman", &v); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(v, []string{"neovim"}) {
		t.Errorf("got %v, want [neovim]", v)
	}

	// No leftover *.tmp* files in the directory after Save.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.Contains(name, ".tmp") {
			t.Errorf("leftover tmp file in state dir: %s", name)
		}
	}
}

func TestAtomicWrite_RoundTripAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := AtomicWrite(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("contents = %q, want hello", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 0600", mode)
	}
}

func TestAtomicWrite_OverwriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")
	if err := AtomicWrite(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("AtomicWrite first: %v", err)
	}
	if err := AtomicWrite(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("AtomicWrite second: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("got %q, want second", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover tmp: %s", e.Name())
		}
	}
}

func TestDefaultPath_UsesXDGStateHomeForNonRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root uses the system state path")
	}
	t.Setenv("XDG_STATE_HOME", "/tmp/state-home")

	got := DefaultPath()
	want := "/tmp/state-home/bigkis/state.json"

	if got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}

func TestDefaultPath_FallsBackToHomeForNonRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root uses the system state path")
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/tmp/home")

	got := DefaultPath()
	want := "/tmp/home/.local/state/bigkis/state.json"

	if got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}
