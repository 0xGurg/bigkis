package node

import (
	"reflect"
	"sort"
	"testing"

	"codeberg.org/gurg/bigkis/internal/config"
)

func TestGroupDeclared_DefaultManager(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{NodeManager: "pnpm"},
		Node: config.Node{
			Packages: []string{"typescript", "eslint"},
		},
	}
	got := groupDeclared(cfg)
	if len(got) != 1 || !reflect.DeepEqual(sorted(got["pnpm"]), []string{"eslint", "typescript"}) {
		t.Errorf("got %v, want pnpm:[eslint typescript]", got)
	}
}

func TestGroupDeclared_PerPackageOverride(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{NodeManager: "pnpm"},
		Node: config.Node{
			Packages: []string{"typescript", "eslint"},
			Package: []config.NodePackage{
				{Name: "@vue/cli", Manager: "yarn"},
				{Name: "create-react-app", Manager: "npm"},
			},
		},
	}
	got := groupDeclared(cfg)
	if !reflect.DeepEqual(sorted(got["yarn"]), []string{"@vue/cli"}) {
		t.Errorf("yarn = %v", got["yarn"])
	}
	if !reflect.DeepEqual(sorted(got["npm"]), []string{"create-react-app"}) {
		t.Errorf("npm = %v", got["npm"])
	}
	if !reflect.DeepEqual(sorted(got["pnpm"]), []string{"eslint", "typescript"}) {
		t.Errorf("pnpm = %v", got["pnpm"])
	}
}

func TestGroupDeclared_OverrideTakesPrecedence(t *testing.T) {
	// typescript is in [node].packages (default pnpm) AND has an override
	// pinning it to npm. Override wins, and it must NOT also appear under pnpm.
	cfg := &config.Config{
		Settings: config.Settings{NodeManager: "pnpm"},
		Node: config.Node{
			Packages: []string{"typescript"},
			Package: []config.NodePackage{
				{Name: "typescript", Manager: "npm"},
			},
		},
	}
	got := groupDeclared(cfg)
	if !reflect.DeepEqual(got["npm"], []string{"typescript"}) {
		t.Errorf("npm = %v, want [typescript]", got["npm"])
	}
	if len(got["pnpm"]) != 0 {
		t.Errorf("pnpm should be empty, got %v", got["pnpm"])
	}
}

func TestGroupDeclared_OverrideWithEmptyManagerFallsBack(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{NodeManager: "pnpm"},
		Node: config.Node{
			Package: []config.NodePackage{
				{Name: "typescript", Manager: ""},
			},
		},
	}
	got := groupDeclared(cfg)
	if !reflect.DeepEqual(got["pnpm"], []string{"typescript"}) {
		t.Errorf("expected fallback to pnpm, got %v", got)
	}
}

func TestGroupDeclared_DedupesWithinSameManager(t *testing.T) {
	cfg := &config.Config{
		Settings: config.Settings{NodeManager: "npm"},
		Node: config.Node{
			Packages: []string{"typescript", "typescript"},
		},
	}
	got := groupDeclared(cfg)
	if len(got["npm"]) != 1 {
		t.Errorf("expected dedup, got %v", got["npm"])
	}
}

func TestInstallArgs(t *testing.T) {
	cases := []struct {
		mgr  string
		want []string
	}{
		{"npm", []string{"install", "-g", "typescript"}},
		{"pnpm", []string{"add", "-g", "typescript"}},
		{"yarn", []string{"global", "add", "typescript"}},
	}
	for _, c := range cases {
		if got := installArgs(c.mgr, []string{"typescript"}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("installArgs(%s) = %v, want %v", c.mgr, got, c.want)
		}
	}
	if got := installArgs("bogus", []string{"x"}); got != nil {
		t.Errorf("installArgs(bogus) = %v, want nil", got)
	}
}

func TestRemoveArgs(t *testing.T) {
	cases := []struct {
		mgr  string
		want []string
	}{
		{"npm", []string{"uninstall", "-g", "typescript"}},
		{"pnpm", []string{"remove", "-g", "typescript"}},
		{"yarn", []string{"global", "remove", "typescript"}},
	}
	for _, c := range cases {
		if got := removeArgs(c.mgr, []string{"typescript"}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("removeArgs(%s) = %v, want %v", c.mgr, got, c.want)
		}
	}
}

func TestAllManagers_UnionOfDeclaredAndPrev(t *testing.T) {
	declared := map[string][]string{"npm": {"a"}}
	prev := persisted{"yarn": {"b"}, "npm": {"c"}}
	got := allManagers(declared, prev)
	want := []string{"npm", "yarn"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
