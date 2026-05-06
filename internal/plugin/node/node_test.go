package node

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
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

// stubLookPath replaces runner.LookPath so HasCommand returns true (or per-name)
// without consulting the real PATH.
func stubLookPath(t *testing.T, available map[string]bool) {
	t.Helper()
	prev := runner.LookPath
	runner.LookPath = func(name string) (string, error) {
		if available == nil || available[name] {
			return "/usr/bin/" + name, nil
		}
		return "", &lookErr{name}
	}
	t.Cleanup(func() { runner.LookPath = prev })
}

type lookErr struct{ name string }

func (e *lookErr) Error() string { return "not found: " + e.name }

func TestProbeNPMLike_ParsesNpmObject(t *testing.T) {
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return `{"dependencies":{"typescript":{},"eslint":{}}}`, "", 0, nil
	}
	got, err := probeNPMLike(f.Runner, "npm")
	if err != nil {
		t.Fatalf("probeNPMLike: %v", err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"eslint", "typescript"}) {
		t.Errorf("got %v", got)
	}
}

func TestProbeNPMLike_ParsesPnpmArray(t *testing.T) {
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return `[{"dependencies":{"typescript":{}}},{"dependencies":{"eslint":{}}}]`, "", 0, nil
	}
	got, err := probeNPMLike(f.Runner, "pnpm")
	if err != nil {
		t.Fatalf("probeNPMLike: %v", err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"eslint", "typescript"}) {
		t.Errorf("got %v", got)
	}
}

func TestProbeNPMLike_AcceptsValidJSONOnNonZeroExit(t *testing.T) {
	// npm exits non-zero when peer-dep complaints exist but still emits
	// valid JSON on stdout. probeNPMLike should parse it.
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return `{"dependencies":{"typescript":{}}}`, "peer dep warning", 1,
			runner.NewExitError(1, "exit status 1")
	}
	got, err := probeNPMLike(f.Runner, "npm")
	if err != nil {
		t.Fatalf("expected JSON parse to succeed despite non-zero exit, got %v", err)
	}
	if !reflect.DeepEqual(got, []string{"typescript"}) {
		t.Errorf("got %v", got)
	}
}

func TestProbeNPMLike_SurfacesErrorWhenStdoutEmpty(t *testing.T) {
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return "", "boom", 2, runner.NewExitError(2, "exit status 2")
	}
	if _, err := probeNPMLike(f.Runner, "npm"); err == nil {
		t.Error("expected error when stdout empty and exit non-zero")
	}
}

func TestProbeNPMLike_SurfacesParseErrorIncludingStderr(t *testing.T) {
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return "not JSON at all", "stderr message", 1,
			runner.NewExitError(1, "exit status 1")
	}
	_, err := probeNPMLike(f.Runner, "npm")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "stderr message") && !strings.Contains(err.Error(), "exit status") {
		t.Errorf("error should mention stderr or exit status; got %v", err)
	}
}

func TestProbeYarn_ParsesInfoLines(t *testing.T) {
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return `info "typescript@5.4.0" has binaries:` + "\n" +
			`info "eslint@9.0.0" has binaries:` + "\n" +
			`some other line` + "\n", "", 0, nil
	}
	got, err := probeYarn(f.Runner)
	if err != nil {
		t.Fatalf("probeYarn: %v", err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"eslint", "typescript"}) {
		t.Errorf("got %v", got)
	}
}

func TestPlan_BuildsReportWithViaManager(t *testing.T) {
	stubLookPath(t, map[string]bool{"npm": true})
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		// Empty global set -> all declared become adds.
		return `{"dependencies":{}}`, "", 0, nil
	}
	n := New()
	n.SetRunner(f.Runner)
	cfg := &config.Config{
		Settings: config.Settings{NodeManager: "npm"},
		Node:     config.Node{Packages: []string{"typescript"}},
	}
	report, err := n.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(report.Operations) != 1 || report.Operations[0].Detail != "via npm" {
		t.Errorf("ops = %+v, want 1 op with detail 'via npm'", report.Operations)
	}
}
