package node

import (
	"bytes"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
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

// TestPlan_FailsWhenDeclaredManagerMissing guards against the regression
// where probeManager silently returned an empty actual set for a missing
// manager and Plan reported "in sync" right up until apply blew up.
func TestPlan_FailsWhenDeclaredManagerMissing(t *testing.T) {
	stubLookPath(t, map[string]bool{"npm": true, "pnpm": false, "yarn": false})
	f := runner.NewFake()
	n := New()
	n.SetRunner(f.Runner)
	cfg := &config.Config{
		Settings: config.Settings{NodeManager: "pnpm"},
		Node:     config.Node{Packages: []string{"typescript"}},
	}
	_, err := n.Plan(cfg, &state.State{})
	if err == nil || !strings.Contains(err.Error(), "pnpm") {
		t.Fatalf("expected missing-pnpm error, got %v", err)
	}
}

// TestPlan_FailsWhenPrevManagerMissing covers the case where a manager was
// previously used (and so appears in state) but has since been uninstalled.
// We must surface that during planning so the user sees it before confirming.
func TestPlan_FailsWhenPrevManagerMissing(t *testing.T) {
	stubLookPath(t, map[string]bool{"npm": true, "pnpm": false, "yarn": false})
	st := &state.State{}
	if err := st.Set("node", persisted{"pnpm": []string{"typescript"}}); err != nil {
		t.Fatal(err)
	}
	f := runner.NewFake()
	n := New()
	n.SetRunner(f.Runner)
	cfg := &config.Config{Settings: config.Settings{NodeManager: "npm"}}
	_, err := n.Plan(cfg, st)
	if err == nil || !strings.Contains(err.Error(), "pnpm") {
		t.Fatalf("expected missing-pnpm error from prev-state, got %v", err)
	}
}

func TestApply_AcceptsSubsetReport(t *testing.T) {
	stubLookPath(t, map[string]bool{"npm": true})
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return `{"dependencies":{}}`, "", 0, nil
	}
	n := New()
	n.SetRunner(f.Runner)
	cfg := &config.Config{
		Settings: config.Settings{NodeManager: "npm"},
		Node:     config.Node{Packages: []string{"typescript", "eslint"}},
	}
	report, err := n.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(report.Operations) < 2 {
		t.Fatalf("expected at least 2 ops, got %d", len(report.Operations))
	}
	// Trim to a subset (keep only the first op).
	report.Operations = report.Operations[:1]

	applyF := runner.NewFake()
	if err := n.Apply(cfg, &state.State{}, report, applyF.Runner, silentNodeUI()); err != nil {
		t.Fatalf("Apply with subset report should succeed, got: %v", err)
	}
}

func TestUpgrade_PerReferencedManager(t *testing.T) {
	stubLookPath(t, map[string]bool{"npm": true, "pnpm": true, "yarn": true})
	n := New()
	rf := runner.NewFake()
	cfg := &config.Config{
		Settings: config.Settings{NodeManager: "npm"},
		Node: config.Node{
			Packages: []string{"typescript"},
			Package: []config.NodePackage{
				{Name: "eslint", Manager: "pnpm"},
				{Name: "cowsay", Manager: "yarn"},
			},
		},
	}
	if err := n.Upgrade(cfg, &state.State{}, rf.Runner, silentNodeUI()); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(rf.Calls) != 3 {
		t.Fatalf("got %d calls: %+v", len(rf.Calls), rf.Calls)
	}
	want := []struct {
		name string
		args []string
	}{
		{"npm", []string{"update", "-g"}},
		{"pnpm", []string{"update", "-g"}},
		{"yarn", []string{"global", "upgrade"}},
	}
	for i, w := range want {
		c := rf.Calls[i]
		if c.Name != w.name || c.Sudo || c.User != "" {
			t.Errorf("call %d: %+v", i, c)
		}
		if !reflect.DeepEqual(c.Args, w.args) {
			t.Errorf("call %d argv = %v, want %v", i, c.Args, w.args)
		}
	}
}

func TestUpgrade_UsesPrevStateWhenDeclaredEmpty(t *testing.T) {
	stubLookPath(t, map[string]bool{"pnpm": true, "npm": false})
	st := &state.State{}
	if err := st.Set("node", persisted{"pnpm": []string{"leftover"}}); err != nil {
		t.Fatal(err)
	}
	n := New()
	rf := runner.NewFake()
	cfg := &config.Config{Settings: config.Settings{NodeManager: "npm"}}
	if err := n.Upgrade(cfg, st, rf.Runner, silentNodeUI()); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(rf.Calls) != 1 || rf.Calls[0].Name != "pnpm" {
		t.Fatalf("calls: %+v", rf.Calls)
	}
	if !reflect.DeepEqual(rf.Calls[0].Args, []string{"update", "-g"}) {
		t.Errorf("args = %v", rf.Calls[0].Args)
	}
}

func TestUpgrade_SkipsWhenNoPackagesTracked(t *testing.T) {
	stubLookPath(t, map[string]bool{"npm": true})
	n := New()
	rf := runner.NewFake()
	cfg := &config.Config{Settings: config.Settings{NodeManager: "npm"}}
	if err := n.Upgrade(cfg, &state.State{}, rf.Runner, silentNodeUI()); err != nil {
		t.Fatal(err)
	}
	if len(rf.Calls) != 0 {
		t.Errorf("expected no upgrade calls, got %+v", rf.Calls)
	}
}

func silentNodeUI() *ui.UI {
	return ui.New(io.Discard, &bytes.Buffer{}, false, true)
}

func TestPendingUpgrades_NPMParsesJSON(t *testing.T) {
	prev := runner.LookPath
	runner.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() { runner.LookPath = prev })

	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "npm" && len(args) >= 3 && args[0] == "outdated" {
			jsonOut := `{"eslint":{"current":"8.50.0","wanted":"8.57.0","latest":"9.0.0"},"prettier":{"current":"3.1.0","wanted":"3.3.0","latest":"3.3.0"}}`
			return jsonOut, "", 0, nil
		}
		return "", "", 0, nil
	}
	n := New()
	cfg := &config.Config{
		Node: config.Node{
			Package: []config.NodePackage{
				{Name: "eslint", Manager: "npm"},
				{Name: "prettier", Manager: "npm"},
			},
		},
	}
	rep, err := n.PendingUpgrades(cfg, f.Runner)
	if err != nil {
		t.Fatalf("PendingUpgrades: %v", err)
	}
	if rep.Plugin != "node" {
		t.Errorf("Plugin = %q", rep.Plugin)
	}
	if len(rep.Operations) != 2 {
		t.Fatalf("got %d ops, want 2", len(rep.Operations))
	}
	// Check first op (alphabetical: eslint before prettier)
	if rep.Operations[0].Target != "eslint" {
		t.Errorf("Target[0] = %q", rep.Operations[0].Target)
	}
	if rep.Operations[0].Kind != plugin.OpUpdate {
		t.Error("should be OpUpdate")
	}
	if !strings.Contains(rep.Operations[0].Detail, "8.50.0") {
		t.Error("missing current version")
	}
	if !strings.Contains(rep.Operations[0].Detail, "via npm") {
		t.Error("missing 'via npm'")
	}
	if rep.Operations[1].Target != "prettier" {
		t.Errorf("Target[1] = %q", rep.Operations[1].Target)
	}
	if !rep.HasUpgrades() {
		t.Error("HasUpgrades should be true")
	}
}

func TestPendingUpgrades_YarnParsesTable(t *testing.T) {
	prev := runner.LookPath
	runner.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() { runner.LookPath = prev })

	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		if name == "yarn" && len(args) >= 1 && args[0] == "outdated" {
			return "Package  Current  Wanted  Latest  URL\neslint   8.50.0  8.57.0  9.0.0   https://eslint.org\n", "", 0, nil
		}
		return "", "", 0, nil
	}
	n := New()
	cfg := &config.Config{
		Node: config.Node{
			Package: []config.NodePackage{
				{Name: "eslint", Manager: "yarn"},
			},
		},
	}
	rep, err := n.PendingUpgrades(cfg, f.Runner)
	if err != nil {
		t.Fatalf("PendingUpgrades: %v", err)
	}
	if len(rep.Operations) != 1 {
		t.Fatalf("got %d ops, want 1", len(rep.Operations))
	}
	if rep.Operations[0].Target != "eslint" {
		t.Errorf("Target = %q", rep.Operations[0].Target)
	}
	if !strings.Contains(rep.Operations[0].Detail, "via yarn") {
		t.Error("missing 'via yarn'")
	}
}

func TestPendingUpgrades_BestEffortFailsSilently(t *testing.T) {
	prev := runner.LookPath
	runner.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() { runner.LookPath = prev })

	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		// npm outdated fails — should be silently skipped
		return "", "", 1, runner.NewExitError(1, "outdated failed")
	}
	n := New()
	cfg := &config.Config{
		Node: config.Node{
			Package: []config.NodePackage{
				{Name: "eslint", Manager: "npm"},
			},
		},
	}
	rep, err := n.PendingUpgrades(cfg, f.Runner)
	if err != nil {
		t.Fatalf("best-effort should not return error: %v", err)
	}
	if len(rep.Operations) != 0 {
		t.Errorf("expected 0 ops on failure, got %d", len(rep.Operations))
	}
}

func TestPendingUpgrades_NoDeclaredPackages(t *testing.T) {
	prev := runner.LookPath
	runner.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() { runner.LookPath = prev })

	n := New()
	cfg := &config.Config{Node: config.Node{Packages: []string{}}}
	rep, err := n.PendingUpgrades(cfg, runner.NewFake().Runner)
	if err != nil {
		t.Fatalf("PendingUpgrades: %v", err)
	}
	if len(rep.Operations) != 0 {
		t.Errorf("expected 0 ops, got %d", len(rep.Operations))
	}
	if rep.HasUpgrades() {
		t.Error("HasUpgrades should be false")
	}
}
