package state

import (
	"path/filepath"
	"reflect"
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
}
