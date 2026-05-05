package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/urfave/cli/v2"

	"github.com/georgepagarigan/bigkis/internal/config"
	"github.com/georgepagarigan/bigkis/internal/plugin"
	"github.com/georgepagarigan/bigkis/internal/plugin/aur"
	"github.com/georgepagarigan/bigkis/internal/plugin/flatpak"
	"github.com/georgepagarigan/bigkis/internal/plugin/node"
	"github.com/georgepagarigan/bigkis/internal/plugin/pacman"
	"github.com/georgepagarigan/bigkis/internal/runner"
	"github.com/georgepagarigan/bigkis/internal/state"
	"github.com/georgepagarigan/bigkis/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app := &cli.App{
		Name:    "bigkis",
		Usage:   "declarative package manager for Arch Linux (pacman, AUR, flatpak, node)",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "path to system.toml (overrides search path)",
				EnvVars: []string{"BIGKIS_CONFIG"},
			},
		},
		Commands: []*cli.Command{
			applyCommand(),
			statusCommand(),
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func applyCommand() *cli.Command {
	return &cli.Command{
		Name:  "apply",
		Usage: "converge the system to match the declared TOML configuration",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Aliases: []string{"n"}, Usage: "show what would change without making changes"},
			&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "skip the confirmation prompt"},
			&cli.StringSliceFlag{Name: "only", Usage: "only run these plugins (comma-separated)"},
			&cli.StringSliceFlag{Name: "skip", Usage: "skip these plugins (comma-separated)"},
		},
		Action: runApply,
	}
}

func statusCommand() *cli.Command {
	return &cli.Command{
		Name:   "status",
		Usage:  "report drift between the declared config and the system, without changing anything",
		Action: runStatus,
	}
}

func runStatus(c *cli.Context) error {
	cfg, st, reg, u, err := bootstrap(c)
	if err != nil {
		return err
	}
	plugins := selectPlugins(cfg.Settings.Enabled, nil, nil, reg, u)
	hasChanges := false
	for _, p := range plugins {
		if err := p.Available(); err != nil {
			u.Warn("%s: unavailable (%v); skipping", p.Name(), err)
			continue
		}
		report, err := p.Plan(cfg, st)
		if err != nil {
			return fmt.Errorf("%s plan: %w", p.Name(), err)
		}
		if !report.HasChanges() {
			u.Info("%s: in sync", p.Name())
			continue
		}
		hasChanges = true
		u.Info("%s: %d change(s)", p.Name(), len(report.Operations))
		printReport(u, report)
	}
	if !hasChanges {
		u.Info("system matches declaration")
	}
	return nil
}

func runApply(c *cli.Context) error {
	cfg, st, reg, u, err := bootstrap(c)
	if err != nil {
		return err
	}
	dryRun := c.Bool("dry-run")
	if c.Bool("yes") {
		u.SetAssumeYes(true)
	}
	only := splitCSV(c.StringSlice("only"))
	skip := splitCSV(c.StringSlice("skip"))

	plugins := selectPlugins(cfg.Settings.Enabled, only, skip, reg, u)
	if len(plugins) == 0 {
		u.Warn("no plugins selected; nothing to do")
		return nil
	}

	type stage struct {
		p      plugin.Plugin
		report plugin.Report
	}
	stages := make([]stage, 0, len(plugins))
	overallChanges := false

	u.Info("planning")
	for _, p := range plugins {
		if err := p.Available(); err != nil {
			u.Warn("%s: unavailable (%v); skipping", p.Name(), err)
			continue
		}
		report, err := p.Plan(cfg, st)
		if err != nil {
			return fmt.Errorf("%s plan: %w", p.Name(), err)
		}
		if !report.HasChanges() {
			u.Step("%s: in sync", p.Name())
			continue
		}
		overallChanges = true
		u.Step("%s: %d change(s)", p.Name(), len(report.Operations))
		printReport(u, report)
		stages = append(stages, stage{p: p, report: report})
	}

	if !overallChanges {
		u.Info("system matches declaration; nothing to do")
		return nil
	}

	if dryRun {
		u.Info("dry-run: not applying")
		return nil
	}

	if !u.Confirm("proceed with these changes?") {
		u.Info("aborted by user")
		return nil
	}

	r := runner.New(false)
	u.Info("applying")
	for _, s := range stages {
		u.Info("plugin: %s", s.p.Name())
		if err := s.p.Apply(cfg, st, r, u); err != nil {
			return fmt.Errorf("%s apply: %w", s.p.Name(), err)
		}
		if err := s.p.PersistState(cfg, st); err != nil {
			return fmt.Errorf("%s persist state: %w", s.p.Name(), err)
		}
	}

	statePath := state.DefaultPath()
	if err := state.Save(statePath, st); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	u.Dim("state saved to %s", statePath)
	u.Info("done")
	return nil
}

// bootstrap loads config + state and constructs the plugin registry.
func bootstrap(c *cli.Context) (*config.Config, *state.State, *plugin.Registry, *ui.UI, error) {
	u := ui.Default(false)

	cfg, err := config.Load(c.String("config"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	u.Dim("config: %s", cfg.Path)

	statePath := state.DefaultPath()
	st, err := state.Load(statePath)
	if err != nil {
		// A missing or unreadable state file is not fatal at this level; Load
		// already turns ENOENT into an empty state.
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil, nil, err
		}
	}

	reg := plugin.NewRegistry()
	reg.Register(pacman.New())
	reg.Register(aur.New())
	reg.Register(flatpak.New())
	reg.Register(node.New())

	return cfg, st, reg, u, nil
}

// selectPlugins resolves the final ordered list of plugins, applying
// `--only` and `--skip` on top of `settings.enabled`. Names in --only or
// --skip that don't match an enabled plugin produce a warning so a typo
// like `--only pacaman` doesn't silently end up running nothing.
func selectPlugins(enabled, only, skip []string, reg *plugin.Registry, u *ui.UI) []plugin.Plugin {
	enabledSet := toSet(enabled)
	for _, name := range only {
		if _, ok := enabledSet[name]; !ok {
			u.Warn("--only %q: not in settings.enabled (%v); ignored", name, enabled)
		}
	}
	for _, name := range skip {
		if _, ok := enabledSet[name]; !ok {
			u.Warn("--skip %q: not in settings.enabled (%v); ignored", name, enabled)
		}
	}

	skipSet := toSet(skip)
	onlySet := toSet(only)

	var out []plugin.Plugin
	for _, name := range enabled {
		if len(onlySet) > 0 {
			if _, ok := onlySet[name]; !ok {
				continue
			}
		}
		if _, skipped := skipSet[name]; skipped {
			continue
		}
		p, ok := reg.Get(name)
		if !ok {
			u.Warn("plugin %q is enabled in config but unknown; skipping", name)
			continue
		}
		out = append(out, p)
	}
	return out
}

func printReport(u *ui.UI, r plugin.Report) {
	ops := append([]plugin.Operation(nil), r.Operations...)
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Kind != ops[j].Kind {
			return ops[i].Kind < ops[j].Kind
		}
		return ops[i].Target < ops[j].Target
	})
	for _, op := range ops {
		label := op.Target
		if op.Detail != "" {
			label = fmt.Sprintf("%s (%s)", op.Target, op.Detail)
		}
		switch op.Kind {
		case plugin.OpAdd:
			u.Add("%s", label)
		case plugin.OpRemove:
			u.Remove("%s", label)
		}
	}
}

func splitCSV(in []string) []string {
	var out []string
	for _, item := range in {
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func toSet(items []string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, x := range items {
		m[x] = struct{}{}
	}
	return m
}
