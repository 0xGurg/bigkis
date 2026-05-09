package flatpak

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
)

func silentUI() *ui.UI { return ui.New(io.Discard, &bytes.Buffer{}, false, true) }

func stubLookPath(t *testing.T) {
	t.Helper()
	prev := runner.LookPath
	runner.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() { runner.LookPath = prev })
}

func TestSplitLines_StripsHeader(t *testing.T) {
	in := "Application ID\norg.mozilla.firefox\norg.gnome.Calendar\n"
	got := splitLines(in)
	want := []string{"org.mozilla.firefox", "org.gnome.Calendar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitLines = %v, want %v", got, want)
	}
}

func TestSplitLines_HandlesEmpty(t *testing.T) {
	if got := splitLines(""); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestProbeUser_RejectsUnsafeUsername(t *testing.T) {
	f := runner.NewFake()
	flat := New()
	flat.SetRunner(f.Runner)
	if _, err := flat.probeUser(f.Runner, "alice; rm -rf /"); err == nil {
		t.Error("expected error for unsafe username")
	}
	if len(f.Calls) != 0 {
		t.Errorf("unsafe username should not run any command; got %+v", f.Calls)
	}
}

func TestProbeUser_UsesArgvSudoNoShell(t *testing.T) {
	f := runner.NewFake()
	f.Respond = func(name string, args []string) (string, string, int, error) {
		return "Application ID\ncom.valvesoftware.Steam\n", "", 0, nil
	}
	flat := New()
	got, err := flat.probeUser(f.Runner, "alice")
	if err != nil {
		t.Fatalf("probeUser: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"com.valvesoftware.Steam"}) {
		t.Errorf("got %v", got)
	}
	if len(f.Calls) != 1 {
		t.Fatalf("expected 1 call, got %+v", f.Calls)
	}
	if f.Calls[0].Name != "sudo" {
		t.Errorf("expected sudo, got %q", f.Calls[0].Name)
	}
	wantArgs := []string{"-u", "alice", "flatpak", "list", "--app", "--user", "--columns=application"}
	if !reflect.DeepEqual(f.Calls[0].Args, wantArgs) {
		t.Errorf("argv = %v, want %v", f.Calls[0].Args, wantArgs)
	}
}

func TestApply_UsesConfigurableRemote(t *testing.T) {
	stubLookPath(t)
	planF := runner.NewFake()
	planF.Respond = func(name string, args []string) (string, string, int, error) {
		// flatpak list --app --system: nothing installed, so declared package
		// becomes an Add.
		return "", "", 0, nil
	}
	flat := New()
	flat.SetRunner(planF.Runner)
	cfg := &config.Config{
		Flatpak: config.Flatpak{
			Packages: []string{"org.mozilla.firefox"},
			Remote:   "fedora",
		},
	}
	report, err := flat.Plan(cfg, &state.State{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	applyF := runner.NewFake()
	if err := flat.Apply(cfg, &state.State{}, report, applyF.Runner, silentUI()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applyF.Calls) != 1 {
		t.Fatalf("expected 1 call, got %+v", applyF.Calls)
	}
	got := applyF.Calls[0]
	if got.Name != "flatpak" || !got.Sudo {
		t.Errorf("expected sudo flatpak, got %+v", got)
	}
	wantArgs := []string{"install", "--system", "--noninteractive", "--assumeyes", "fedora", "org.mozilla.firefox"}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Errorf("argv = %v, want %v (custom remote not respected)", got.Args, wantArgs)
	}
}

func TestApply_RejectsCallBeforePlan(t *testing.T) {
	flat := New()
	err := flat.Apply(&config.Config{}, &state.State{}, plugin.Report{}, runner.NewFake().Runner, silentUI())
	if err == nil || !strings.Contains(err.Error(), "before Plan") {
		t.Errorf("expected before-Plan error, got %v", err)
	}
}

func TestUpgrade_SystemAndDeclaredUsers(t *testing.T) {
	stubLookPath(t)
	f := New()
	cfg := &config.Config{
		Flatpak: config.Flatpak{
			UserPackages: map[string][]string{
				"bob":   {"org.bob.App"},
				"alice": {"org.alice.App"},
			},
		},
	}
	rf := runner.NewFake()
	if err := f.Upgrade(cfg, &state.State{}, rf.Runner, silentUI()); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(rf.Calls) != 3 {
		t.Fatalf("expected 3 calls, got %+v", rf.Calls)
	}
	if rf.Calls[0].Name != "flatpak" || !rf.Calls[0].Sudo {
		t.Errorf("call 0: %+v", rf.Calls[0])
	}
	wantSys := []string{"update", "--system", "--noninteractive", "--assumeyes"}
	if !reflect.DeepEqual(rf.Calls[0].Args, wantSys) {
		t.Errorf("system argv = %v", rf.Calls[0].Args)
	}
	if rf.Calls[1].Name != "flatpak" || rf.Calls[1].Sudo || rf.Calls[1].User != "alice" {
		t.Errorf("call 1: %+v", rf.Calls[1])
	}
	wantUser := []string{"update", "--user", "--noninteractive", "--assumeyes"}
	if !reflect.DeepEqual(rf.Calls[1].Args, wantUser) {
		t.Errorf("alice argv = %v", rf.Calls[1].Args)
	}
	if rf.Calls[2].User != "bob" {
		t.Errorf("call 2 user = %q", rf.Calls[2].User)
	}
}

func TestUpgrade_RejectsUnsafeUsername(t *testing.T) {
	stubLookPath(t)
	f := New()
	cfg := &config.Config{
		Flatpak: config.Flatpak{
			UserPackages: map[string][]string{"user;drop": {"org.foo"}},
		},
	}
	err := f.Upgrade(cfg, &state.State{}, runner.NewFake().Runner, silentUI())
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("expected unsafe username error, got %v", err)
	}
}
