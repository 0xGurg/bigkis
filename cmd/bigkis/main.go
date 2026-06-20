package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/urfave/cli/v2"

	"github.com/0xGurg/bigkis/internal/config"
	"github.com/0xGurg/bigkis/internal/doctor"
	"github.com/0xGurg/bigkis/internal/explain"
	"github.com/0xGurg/bigkis/internal/importer"
	"github.com/0xGurg/bigkis/internal/lockfile"
	"github.com/0xGurg/bigkis/internal/plugin"
	"github.com/0xGurg/bigkis/internal/plugin/aur"
	"github.com/0xGurg/bigkis/internal/plugin/flatpak"
	"github.com/0xGurg/bigkis/internal/plugin/node"
	"github.com/0xGurg/bigkis/internal/plugin/pacman"
	"github.com/0xGurg/bigkis/internal/rollback"
	"github.com/0xGurg/bigkis/internal/runner"
	"github.com/0xGurg/bigkis/internal/state"
	"github.com/0xGurg/bigkis/internal/tui"
	applyreview "github.com/0xGurg/bigkis/internal/tui/apply"
	tuirollback "github.com/0xGurg/bigkis/internal/tui/rollback"
	"github.com/0xGurg/bigkis/internal/tui/status"
	"github.com/0xGurg/bigkis/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// Exit codes. Zero means success. Non-zero codes are stable so wrappers
// can branch on them; see wiki/Exit-Codes.md for the user-facing contract.
const (
	ExitOK            = 0
	ExitError         = 1
	ExitUserCancelled = 2
	ExitDrift         = 3
)

func main() {
	app := &cli.App{
		Name:                 "bigkis",
		Usage:                "declarative package manager for Arch Linux (pacman, AUR, flatpak, node)",
		Version:              version,
		EnableBashCompletion: true,
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
			checkCommand(),
			doctorCommand(),
			importCommand(),
			explainCommand(),
			rollbackCommand(),
			completionCommand(),
		},
	}

	if err := app.Run(os.Args); err != nil {
		// cli.Exit / cli.ExitCoder values carry an exit code we want to use
		// as-is (e.g. ExitUserCancelled). Anything else is a real error.
		if ec, ok := err.(cli.ExitCoder); ok {
			if msg := err.Error(); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			os.Exit(ec.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(ExitError)
	}
}

func applyCommand() *cli.Command {
	return &cli.Command{
		Name:  "apply",
		Usage: "converge the system to match the declared TOML configuration",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Aliases: []string{"n"}, Usage: "show what would change without making changes"},
			&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "skip the confirmation prompt"},
			&cli.BoolFlag{Name: "quiet", Aliases: []string{"q"}, Usage: "suppress informational output (errors/warnings still printed)"},
			&cli.BoolFlag{Name: "json", Usage: "emit a JSON plan to stdout and exit (does not apply; logs go to stderr)"},
			&cli.BoolFlag{Name: "no-rollback", Usage: "do not write a rollback script for this apply"},
			&cli.BoolFlag{Name: "no-lock", Usage: "do not write bigkis.lock after apply"},
			&cli.StringFlag{Name: "lock", Usage: "path to write bigkis.lock (default: next to config)"},
			&cli.StringSliceFlag{Name: "only", Usage: "only run these plugins (comma-separated)"},
			&cli.StringSliceFlag{Name: "skip", Usage: "skip these plugins (comma-separated)"},
			&cli.BoolFlag{Name: "no-upgrade", Usage: "skip system package upgrades (only install/remove to match the declaration)"},
			&cli.BoolFlag{Name: "select", Usage: "interactive review with per-operation checkboxes (requires --interactive TTY)"},
		},
		Action: runApply,
	}
}

func statusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "report drift between the declared config and the system, without changing anything",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "emit machine-readable JSON to stdout (logs go to stderr)"},
			&cli.BoolFlag{Name: "quiet", Usage: "suppress info-level log output"},
			&cli.BoolFlag{Name: "exit-on-drift", Usage: "exit with code 3 instead of 0 when drift is detected"},
			&cli.StringSliceFlag{Name: "only", Usage: "only check these plugins (comma-separated)"},
			&cli.StringSliceFlag{Name: "skip", Usage: "skip these plugins (comma-separated)"},
			&cli.BoolFlag{Name: "upgrades", Usage: "also show packages with newer versions available (may be slow — requires network)"},
		},
		Action: runStatus,
	}
}

func checkCommand() *cli.Command {
	return &cli.Command{
		Name:  "check",
		Usage: "validate the config (parse, schema, includes, host overlay, groups). No state, no system access.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "emit machine-readable JSON to stdout"},
		},
		Action: func(c *cli.Context) error {
			cfg, err := config.Load(c.String("config"))
			if err != nil {
				return err
			}

			if c.Bool("json") {
				type checkResult struct {
					OK    bool     `json:"ok"`
					Path  string   `json:"path"`
					Files []string `json:"files"`
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(checkResult{
					OK:    true,
					Path:  cfg.Path,
					Files: cfg.SourcePaths,
				})
			}

			fmt.Printf("ok: %s\n", cfg.Path)
			if len(cfg.SourcePaths) > 1 {
				fmt.Println("included:")
				for _, p := range cfg.SourcePaths[1:] {
					fmt.Printf("  - %s\n", p)
				}
			}
			return nil
		},
	}
}

func doctorCommand() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "run preflight checks on the host and config",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "emit machine-readable JSON to stdout"},
		},
		Action: runDoctor,
	}
}

func runDoctor(c *cli.Context) error {
	cfg, cfgErr := config.Load(c.String("config"))
	env := doctor.DefaultEnv(state.DefaultPath(), rollback.Dir())
	report := doctor.Run(cfg, cfgErr, env)

	if c.Bool("json") {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
		if !report.OK {
			return cli.Exit("", ExitError)
		}
		return nil
	}

	fmt.Print(report.Render())
	if !report.OK {
		return cli.Exit("", ExitError)
	}
	return nil
}

func importCommand() *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "scan the current system and emit a starter system.toml on stdout",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "write to a file instead of stdout"},
			&cli.StringSliceFlag{Name: "only", Usage: "only import these plugins (comma-separated)"},
			&cli.StringFlag{Name: "aur-helper", Value: "yay", Usage: "value to write for [settings].aur_helper"},
			&cli.StringFlag{Name: "node-manager", Value: "npm", Usage: "value to write for [settings].node_manager"},
			&cli.BoolFlag{Name: "interactive", Aliases: []string{"i"}, Usage: "interactive package picker (requires TTY)"},
		},
		Action: func(c *cli.Context) error {
			opts := importer.Options{
				Only:        splitCSV(c.StringSlice("only")),
				AURHelper:   c.String("aur-helper"),
				NodeManager: c.String("node-manager"),
			}

			// Compute output writer once — used in both branches below.
			out := io.Writer(os.Stdout)
			if path := c.String("output"); path != "" {
				f, err := os.Create(path)
				if err != nil {
					return err
				}
				defer f.Close()
				out = f
			}

			if c.Bool("interactive") && tui.ShouldUse(os.Stdout, os.Stdin, false, false) {
				model := importer.NewImportPicker(opts)
				// ImportPicker embeds *importPickerModel so mutations during
				// program.Run() are visible through the original model reference.
				// Do not change to a value receiver on importPickerModel.Update
				// without updating this code.
				program := tui.NewProgram(model)
				if _, err := program.Run(); err != nil {
					return err
				}
				if model.Cancelled() {
					return cli.Exit("", ExitUserCancelled)
				}
				return importer.RunSelected(out, opts, model.Selection())
			}

			return importer.Run(out, opts)
		},
	}
}

func explainCommand() *cli.Command {
	return &cli.Command{
		Name:      "explain",
		Usage:     "explain a single package: declared, installed, managed, status",
		ArgsUsage: "<package>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "emit machine-readable JSON to stdout"},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return fmt.Errorf("usage: bigkis explain <package>")
			}
			cfg, st, _, _, err := bootstrap(c)
			if err != nil {
				return err
			}
			r := explain.Inspect(c.Args().First(), cfg, st)

			if c.Bool("json") {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(r)
			}

			fmt.Print(r.Render())
			return nil
		},
	}
}

func rollbackCommand() *cli.Command {
	return &cli.Command{
		Name:  "rollback",
		Usage: "list or run a previous rollback script",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "list", Usage: "list available rollback scripts and exit"},
			&cli.BoolFlag{Name: "latest", Usage: "run the most recent rollback script"},
			&cli.StringFlag{Name: "id", Usage: "run a specific rollback by timestamp id"},
			&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "skip confirmation"},
		},
		Action: func(c *cli.Context) error {
			u := ui.Default(c.Bool("yes"))

			// TUI browser: no specific flags + TTY
			if !c.Bool("list") && !c.Bool("latest") && c.String("id") == "" {
				if tui.ShouldUse(os.Stdout, os.Stdin, c.Bool("json"), c.Bool("quiet")) {
					model, err := tuirollback.NewRollbackBrowser()
					if err != nil {
						return err
					}
					program := tui.NewProgram(model)
					if _, err := program.Run(); err != nil {
						return err
					}
					if model.Confirmed() {
						return rollback.Run(model.RunTarget())
					}
					if err := model.Err(); err != nil {
						return err
					}
					return nil
				}
				// Fall through to existing listing behavior
			}

			scripts, err := rollback.List()
			if err != nil {
				return err
			}
			if c.Bool("list") || (!c.Bool("latest") && c.String("id") == "") {
				if len(scripts) == 0 {
					u.Info("no rollback scripts in %s", rollback.Dir())
					return nil
				}
				u.Info("rollback scripts in %s:", rollback.Dir())
				for _, s := range scripts {
					u.Step("%s  %s", s.ID, s.Path)
				}
				return nil
			}

			var target rollback.Script
			if id := c.String("id"); id != "" {
				found := false
				for _, s := range scripts {
					if s.ID == id {
						target = s
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("no rollback script with id %s", id)
				}
			} else {
				// --latest with no scripts on disk used to panic via the
				// negative-index slice. Surface the empty case cleanly.
				if len(scripts) == 0 {
					u.Info("no rollback scripts in %s", rollback.Dir())
					return nil
				}
				target = scripts[len(scripts)-1]
			}

			u.Warn("about to execute rollback %s (%s)", target.ID, target.Path)
			if !u.Confirm("proceed?") {
				u.Info("aborted by user")
				return nil
			}
			return rollback.Run(target)
		},
	}
}

func completionCommand() *cli.Command {
	return &cli.Command{
		Name:      "completion",
		Usage:     "print a shell completion script (bash, zsh, fish)",
		ArgsUsage: "<bash|zsh|fish>",
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return fmt.Errorf("usage: bigkis completion <bash|zsh|fish>")
			}
			switch c.Args().First() {
			case "bash":
				fmt.Print(bashCompletionScript)
			case "zsh":
				fmt.Print(zshCompletionScript)
			case "fish":
				fmt.Print(fishCompletionScript)
			default:
				return fmt.Errorf("unknown shell: %s (supported: bash, zsh, fish)", c.Args().First())
			}
			return nil
		},
	}
}

func runStatus(c *cli.Context) error {
	if c.Bool("json") {
		return runStatusJSON(c)
	}
	cfg, st, reg, u, err := bootstrap(c)
	if err != nil {
		return err
	}
	only := splitCSV(c.StringSlice("only"))
	skip := splitCSV(c.StringSlice("skip"))
	plugins := selectPlugins(cfg.Settings.Enabled, only, skip, reg, u)

	// ── Collect phase ──
	var statuses []status.PluginStatus
	for _, p := range plugins {
		ps := status.PluginStatus{Name: p.Name()}
		if err := p.Available(cfg); err != nil {
			ps.Available = false
			ps.Error = err.Error()
		} else {
			ps.Available = true
			report, err := p.Plan(cfg, st)
			if err != nil {
				return fmt.Errorf("%s plan: %w", p.Name(), err)
			}
			ps.Report = report
		}
		statuses = append(statuses, ps)
	}

	// ── Collect pending upgrades (opt-in via --upgrades) ──
	if c.Bool("upgrades") {
		r := runner.New(false)
		upgradeReports := collectUpgrades(plugins, cfg, r, u)
		for i := range statuses {
			for _, ur := range upgradeReports {
				if ur.Plugin == statuses[i].Name {
					statuses[i].Upgrades = ur
					break
				}
			}
		}
	}

	// ── TUI branch ──
	if !c.Bool("exit-on-drift") && tui.ShouldUse(os.Stdout, os.Stdin, c.Bool("json"), c.Bool("quiet")) {
		model := status.NewStatusDashboard(cfg.Path, statuses)
		program := tui.NewProgram(model)
		if _, err := program.Run(); err != nil {
			return err
		}
		if model.ApplyRequested() {
			fmt.Fprintln(os.Stderr, "run `sudo bigkis apply` to converge")
		}
		return nil
	}

	// ── Print branch (existing behavior, now using collected data) ──
	hasChanges := false
	var unavailable []string
	for _, ps := range statuses {
		if !ps.Available {
			u.Warn("%s: unavailable (%v); skipping", ps.Name, ps.Error)
			unavailable = append(unavailable, ps.Name)
			continue
		}
		report := ps.Report
		if !report.HasChanges() && !ps.Upgrades.HasUpgrades() {
			u.Info("%s: in sync", ps.Name)
			continue
		}
		if report.HasChanges() {
			hasChanges = true
			u.Info("%s: %d change(s)", ps.Name, len(report.Operations))
			printReport(u, report)
		}
		if ps.Upgrades.HasUpgrades() {
			u.Info("%s: %d upgrade(s) available", ps.Name, len(ps.Upgrades.Operations))
			for _, op := range ps.Upgrades.Operations {
				label := op.Target
				if op.Detail != "" {
					label = fmt.Sprintf("%s  %s", op.Target, op.Detail)
				}
				u.Step("  ↑ %s", label)
			}
		}
	}
	if !hasChanges {
		// Don't claim "system matches declaration" when at least one plugin
		// was skipped: we literally couldn't check those, and silent passes
		// in CI would hide drift introduced by a missing tool.
		if len(unavailable) > 0 {
			u.Warn("system matches declaration for available plugins; %d skipped (%s)", len(unavailable), strings.Join(unavailable, ", "))
		} else {
			u.Info("system matches declaration")
		}
		return nil
	}
	if c.Bool("exit-on-drift") {
		return cli.Exit("drift detected", ExitDrift)
	}
	return nil
}

// runStatusJSON emits a machine-readable JSON report on stdout. All log/UI
// output goes to stderr so the JSON stream stays clean and pipeable.
func runStatusJSON(c *cli.Context) error {
	cfg, st, reg, _, err := bootstrapTo(c, os.Stderr)
	if err != nil {
		return err
	}

	type opJSON struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
		Detail string `json:"detail,omitempty"`
	}
	type pluginJSON struct {
		Name        string   `json:"name"`
		Available   bool     `json:"available"`
		AvailableEr string   `json:"availableError,omitempty"`
		InSync      bool     `json:"inSync"`
		Operations  []opJSON `json:"operations"`
		Upgrades    []opJSON `json:"upgrades,omitempty"`
	}
	type reportJSON struct {
		ConfigPath string `json:"configPath"`
		InSync     bool   `json:"inSync"`
		// Incomplete signals that one or more plugins could not be checked
		// (e.g. their tools are missing). Tooling should treat InSync=true
		// + Incomplete=true as "no detected drift, but this run did not
		// observe every plugin."
		Incomplete bool         `json:"incomplete"`
		Plugins    []pluginJSON `json:"plugins"`
	}

	out := reportJSON{ConfigPath: cfg.Path, InSync: true}

	only := splitCSV(c.StringSlice("only"))
	skip := splitCSV(c.StringSlice("skip"))
	plugins := selectPlugins(cfg.Settings.Enabled, only, skip, reg, ui.New(os.Stderr, os.Stdin, false, true))
	for _, p := range plugins {
		pj := pluginJSON{Name: p.Name(), Available: true, InSync: true}
		if err := p.Available(cfg); err != nil {
			pj.Available = false
			pj.AvailableEr = err.Error()
			out.Incomplete = true
			out.Plugins = append(out.Plugins, pj)
			continue
		}
		report, err := p.Plan(cfg, st)
		if err != nil {
			return fmt.Errorf("%s plan: %w", p.Name(), err)
		}
		pj.InSync = !report.HasChanges()
		if !pj.InSync {
			out.InSync = false
		}
		for _, op := range report.Operations {
			kind := "add"
			if op.Kind == plugin.OpRemove {
				kind = "remove"
			}
			pj.Operations = append(pj.Operations, opJSON{Kind: kind, Target: op.Target, Detail: op.Detail})
		}
		// Collect pending upgrades when --upgrades is set
		if c.Bool("upgrades") {
			ur, err := p.PendingUpgrades(cfg, runner.New(false))
			if err == nil {
				for _, op := range ur.Operations {
					pj.Upgrades = append(pj.Upgrades, opJSON{Kind: "upgrade", Target: op.Target, Detail: op.Detail})
				}
			}
		}
		out.Plugins = append(out.Plugins, pj)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	if !out.InSync && c.Bool("exit-on-drift") {
		return cli.Exit("", ExitDrift)
	}
	return nil
}

func runApply(c *cli.Context) error {
	if c.Bool("json") {
		return runApplyJSON(c)
	}

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

	plan, err := planAll(cfg, st, plugins, u)
	if err != nil {
		return err
	}

	// Plugins that failed Available during planning were already warned;
	// omit them from the upgrade loop so we don't repeat the same warning.
	upgradePlugins := pluginsForUpgrade(plugins, plan.unavailable)

	noUpgrade := c.Bool("no-upgrade")
	rPreview := runner.New(true)
	if dryRun {
		if !noUpgrade {
			u.Info("dry-run: upgrades (no changes applied)")
			if err := runUpgrades(upgradePlugins, cfg, st, rPreview, u); err != nil {
				return err
			}
		}
		if plan.overall {
			u.Info("dry-run: install/remove preview above; not applying")
		} else if noUpgrade {
			u.Info("system matches declaration; --no-upgrade (skipped upgrade preview); nothing to do")
		} else {
			u.Info("system matches declaration; upgrade preview above; no install/remove needed")
		}
		return nil
	}

	statePath := state.DefaultPath()

	// First-run safety: even when there are no stages to apply, persist the
	// declared set so subsequent applies can plan removals. Without this
	// step a clean "everything is already installed" first run leaves
	// lastApplied empty and removals are silently inhibited forever.
	if !plan.overall {
		if noUpgrade {
			u.Info("system matches declaration; --no-upgrade; nothing to do")
			if err := persistInSync(plan.insync, cfg, st, statePath, u); err != nil {
				return err
			}
			return nil
		}
		u.Info("system matches declaration; running upgrades only")
	}

	// TUI plan review: replaces the text confirm prompt with an interactive review.
	// Gated: not --dry-run (already handled), not --yes (user skipped confirm),
	// not --json (JSON path), and terminal is interactive.
	if !dryRun && !c.Bool("yes") && (plan.overall || !noUpgrade) && tui.ShouldUse(os.Stdout, os.Stdin, c.Bool("json"), c.Bool("quiet")) {
		// Collect pending upgrades for TUI display
		var upgradeReports []plugin.UpgradeReport
		if !noUpgrade {
			rProbe := runner.New(false)
			upgradeReports = collectUpgrades(upgradePlugins, cfg, rProbe, u)
		}

		var plans []applyreview.PluginPlan
		for _, s := range plan.stages {
			pp := applyreview.PluginPlan{
				Name:                s.Plugin.Name(),
				InSync:              false,
				Report:              s.Report,
				DependencyInstalled: dependencyInstalledFor(s.Plugin),
			}
			// Attach upgrade report for this plugin
			for _, ur := range upgradeReports {
				if ur.Plugin == s.Plugin.Name() {
					pp.Upgrades = ur
					break
				}
			}
			plans = append(plans, pp)
		}
		for _, p := range plan.insync {
			pp := applyreview.PluginPlan{
				Name:                p.Name(),
				InSync:              true,
				Report:              plugin.Report{},
				DependencyInstalled: dependencyInstalledFor(p),
			}
			for _, ur := range upgradeReports {
				if ur.Plugin == p.Name() {
					pp.Upgrades = ur
					break
				}
			}
			plans = append(plans, pp)
		}
		selective := c.Bool("select")
		model := applyreview.NewApplyReview(cfg.Path, plans, dryRun, !noUpgrade, selective)
		program := tui.NewProgram(model)
		if _, err := program.Run(); err != nil {
			return err
		}
		if model.Cancelled() {
			u.Info("aborted by user")
			return cli.Exit("aborted by user", ExitUserCancelled)
		}
		// Confirmed — fall through to apply logic below.
		// Build the stages to apply. In selective mode, only the checked
		// operations from each plugin are included.
		if selective {
			var filteredStages []stage
			for _, fp := range model.FilteredPlans() {
				if !fp.InSync && len(fp.Report.Operations) > 0 {
					for _, s := range plan.stages {
						if s.Plugin.Name() == fp.Name {
							filteredStages = append(filteredStages, stage{
								Plugin: s.Plugin,
								Report: fp.Report,
							})
							break
						}
					}
				}
			}
			plan.stages = filteredStages
		}
	} else {
		prompt := applyConfirmPrompt(plan.overall, !noUpgrade)
		if !u.Confirm(prompt) {
			u.Info("aborted by user")
			return cli.Exit("aborted by user", ExitUserCancelled)
		}
	}

	r := runner.New(false)
	if !noUpgrade {
		u.Info("upgrading packages")
		if err := runUpgrades(upgradePlugins, cfg, st, r, u); err != nil {
			// Some upgrades failed but others may have succeeded. Continue
			// with apply so that install/remove operations still run for
			// plugins whose upgrades worked. The error is reported at the end.
			u.Warn("some upgrades failed (continuing with apply): %v", err)
		}
	}

	if !plan.overall {
		if err := persistInSync(plan.insync, cfg, st, statePath, u); err != nil {
			return err
		}
		u.Dim("state saved to %s", statePath)
		if !c.Bool("no-lock") {
			lockPath := c.String("lock")
			if lockPath == "" {
				lockPath = lockfile.DefaultPath(cfg.Path)
			}
			if err := lockfile.Write(lockPath, cfg); err != nil {
				u.Warn("could not write lockfile: %v", err)
			} else {
				u.Dim("lockfile: %s", lockPath)
			}
		}
		u.Info("done")
		return nil
	}

	u.Info("applying")
	applied, applyErr := applyStages(plan.stages, cfg, st, statePath, r, u)

	// Always write the rollback script for the stages that actually applied,
	// even on partial failure. Earlier versions wrote the rollback before
	// any stage ran, so a partial-failure rollback would try to undo work
	// that never happened.
	if !c.Bool("no-rollback") {
		var ops []rollback.Op
		for _, s := range applied {
			ops = append(ops, rollback.OpsForReport(s.Plugin.Name(), cfg, s.Report)...)
		}
		if len(ops) > 0 {
			if path, err := rollback.Write(ops); err != nil {
				u.Warn("could not write rollback script: %v", err)
			} else if path != "" {
				u.Dim("rollback script: %s", path)
			}
		}
	}

	if applyErr != nil {
		// Partial failure: still persist state for plugins that succeeded
		// so a subsequent apply doesn't re-plan their work.
		if err := persistInSync(plan.insync, cfg, st, statePath, u); err != nil {
			u.Warn("could not save state after partial failure: %v", err)
		}
		return applyErr
	}

	if err := persistInSync(plan.insync, cfg, st, statePath, u); err != nil {
		return err
	}
	u.Dim("state saved to %s", statePath)

	if !c.Bool("no-lock") {
		lockPath := c.String("lock")
		if lockPath == "" {
			lockPath = lockfile.DefaultPath(cfg.Path)
		}
		if err := lockfile.Write(lockPath, cfg); err != nil {
			u.Warn("could not write lockfile: %v", err)
		} else {
			u.Dim("lockfile: %s", lockPath)
		}
	}

	u.Info("done")
	return nil
}

// stage pairs a plugin with the report it produced from Plan, so Apply can
// execute exactly what the user confirmed.
type stage struct {
	Plugin plugin.Plugin
	Report plugin.Report
}

type dependencyInstalledPlugin interface {
	DependencyInstalled() []string
}

func dependencyInstalledFor(p plugin.Plugin) []string {
	withDeps, ok := p.(dependencyInstalledPlugin)
	if !ok {
		return nil
	}
	return withDeps.DependencyInstalled()
}

// planResult is the structured outcome of planning every selected plugin.
// Stages carry the plugins with pending changes; insync carries the plugins
// that were available but already in sync (we still need their declared set
// recorded so future applies can plan removals); unavailable just records
// what we skipped so the orchestrator can surface "incomplete" results.
type planResult struct {
	stages      []stage
	insync      []plugin.Plugin
	unavailable []string
	overall     bool
}

// planAll runs Plan for each plugin. Plugins whose Available() returns an
// error are skipped with a warning and recorded in unavailable.
func planAll(cfg *config.Config, st *state.State, plugins []plugin.Plugin, u *ui.UI) (planResult, error) {
	var res planResult
	u.Info("planning")
	for _, p := range plugins {
		if err := p.Available(cfg); err != nil {
			u.Warn("%s: unavailable (%v); skipping", p.Name(), err)
			res.unavailable = append(res.unavailable, p.Name())
			continue
		}
		report, err := p.Plan(cfg, st)
		if err != nil {
			return planResult{}, fmt.Errorf("%s plan: %w", p.Name(), err)
		}
		if !report.HasChanges() {
			u.Step("%s: in sync", p.Name())
			res.insync = append(res.insync, p)
			continue
		}
		res.overall = true
		u.Step("%s: %d change(s)", p.Name(), len(report.Operations))
		printReport(u, report)
		res.stages = append(res.stages, stage{Plugin: p, Report: report})
	}
	return res, nil
}

// applyStages runs each stage's Apply + PersistState and checkpoints the
// state file after every successful plugin. If a plugin fails, the error is
// recorded and the remaining plugins still run. It returns the stages that
// completed successfully so the caller can write a rollback script that
// matches reality even on partial failure. A multi-error is returned if any
// stages failed.
func applyStages(stages []stage, cfg *config.Config, st *state.State, statePath string, r *runner.Runner, u *ui.UI) ([]stage, error) {
	var applied []stage
	var errs []string
	for _, s := range stages {
		u.Info("plugin: %s", s.Plugin.Name())
		if err := s.Plugin.Apply(cfg, st, s.Report, r, u); err != nil {
			u.Warn("%s apply failed: %v", s.Plugin.Name(), err)
			errs = append(errs, fmt.Sprintf("%s: %v", s.Plugin.Name(), err))
			continue
		}
		if err := s.Plugin.PersistState(cfg, st); err != nil {
			u.Warn("%s persist state failed: %v", s.Plugin.Name(), err)
			errs = append(errs, fmt.Sprintf("%s persist: %v", s.Plugin.Name(), err))
			// Apply succeeded but persist failed — still count as applied
			// since the system was changed.
		}
		if statePath != "" {
			if err := state.Save(statePath, st); err != nil {
				return applied, fmt.Errorf("checkpoint state after %s: %w", s.Plugin.Name(), err)
			}
		}
		applied = append(applied, s)
	}
	if len(errs) > 0 {
		return applied, fmt.Errorf("apply failures: %s", strings.Join(errs, "; "))
	}
	return applied, nil
}

// persistInSync writes ownership state for plugins that planned no changes.
// Without this, a first-run-clean machine never records what bigkis owns,
// so a later removal from the config would be inhibited by first-run safety
// in plan.Compute. Plugins with pending stages already had PersistState
// called inside applyStages and shouldn't appear in this list.
func persistInSync(plugins []plugin.Plugin, cfg *config.Config, st *state.State, statePath string, u *ui.UI) error {
	if len(plugins) == 0 {
		return nil
	}
	for _, p := range plugins {
		if err := p.PersistState(cfg, st); err != nil {
			return fmt.Errorf("%s persist state: %w", p.Name(), err)
		}
	}
	if statePath != "" {
		if err := state.Save(statePath, st); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}
	return nil
}

// runApplyJSON emits a machine-readable plan to stdout. It does NOT actually
// apply changes; the JSON mode is intended for tooling that wants the same
// shape as `status --json` plus an explicit dryRun flag. Combine with
// --no-rollback / --no-lock to script around bigkis.
//
// When --no-upgrade is not set, pending upgrades are also included in the
// output under each plugin's "upgrades" key.
//
// Exit codes mirror status --exit-on-drift: drift produces ExitDrift (3) so
// scripts can branch on "would-have-applied" without parsing JSON. The wiki
// has documented this for a while; older builds always returned 0.
func runApplyJSON(c *cli.Context) error {
	cfg, st, reg, _, err := bootstrapTo(c, os.Stderr)
	if err != nil {
		return err
	}
	logUI := ui.New(os.Stderr, os.Stdin, false, true)

	type opJSON struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
		Detail string `json:"detail,omitempty"`
	}
	type pluginJSON struct {
		Name        string   `json:"name"`
		Available   bool     `json:"available"`
		AvailableEr string   `json:"availableError,omitempty"`
		InSync      bool     `json:"inSync"`
		Operations  []opJSON `json:"operations"`
		Upgrades    []opJSON `json:"upgrades,omitempty"`
	}
	type reportJSON struct {
		ConfigPath string       `json:"configPath"`
		DryRun     bool         `json:"dryRun"`
		InSync     bool         `json:"inSync"`
		Incomplete bool         `json:"incomplete"`
		Plugins    []pluginJSON `json:"plugins"`
	}

	noUpgrade := c.Bool("no-upgrade")
	out := reportJSON{ConfigPath: cfg.Path, DryRun: true, InSync: true}
	only := splitCSV(c.StringSlice("only"))
	skip := splitCSV(c.StringSlice("skip"))
	plugins := selectPlugins(cfg.Settings.Enabled, only, skip, reg, logUI)
	for _, p := range plugins {
		pj := pluginJSON{Name: p.Name(), Available: true, InSync: true}
		if err := p.Available(cfg); err != nil {
			pj.Available = false
			pj.AvailableEr = err.Error()
			out.Incomplete = true
			out.Plugins = append(out.Plugins, pj)
			continue
		}
		report, err := p.Plan(cfg, st)
		if err != nil {
			return fmt.Errorf("%s plan: %w", p.Name(), err)
		}
		pj.InSync = !report.HasChanges()
		if !pj.InSync {
			out.InSync = false
		}
		for _, op := range report.Operations {
			kind := "add"
			if op.Kind == plugin.OpRemove {
				kind = "remove"
			}
			pj.Operations = append(pj.Operations, opJSON{Kind: kind, Target: op.Target, Detail: op.Detail})
		}
		// Collect pending upgrades when upgrades are enabled
		if !noUpgrade {
			ur, err := p.PendingUpgrades(cfg, runner.New(false))
			if err == nil {
				for _, op := range ur.Operations {
					pj.Upgrades = append(pj.Upgrades, opJSON{Kind: "upgrade", Target: op.Target, Detail: op.Detail})
				}
			}
		}
		out.Plugins = append(out.Plugins, pj)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	if !out.InSync {
		return cli.Exit("", ExitDrift)
	}
	return nil
}

// bootstrap loads config + state and constructs the plugin registry.
func bootstrap(c *cli.Context) (*config.Config, *state.State, *plugin.Registry, *ui.UI, error) {
	return bootstrapTo(c, os.Stdout)
}

// bootstrapTo is bootstrap, but lets the caller route UI output to a specific
// writer (useful so JSON output on stdout is not polluted by logs).
//
// We honor --quiet here (rather than after the fact) so the dim "config: ..."
// trace line stays out of the way when the operator asked for silence.
func bootstrapTo(c *cli.Context, logTo *os.File) (*config.Config, *state.State, *plugin.Registry, *ui.UI, error) {
	quiet := c.Bool("quiet")
	u := ui.New(logTo, os.Stdin, ui.IsColorTTY(logTo), quiet)

	cfg, err := config.Load(c.String("config"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	u.Dim("config: %s", cfg.Path)
	for _, p := range cfg.SourcePaths[1:] {
		u.Dim("  + %s", p)
	}

	statePath := state.DefaultPath()
	st, err := state.Load(statePath)
	if err != nil {
		// A missing or unreadable state file is not fatal at this level; Load
		// already turns ENOENT into an empty state.
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil, nil, err
		}
	}

	var reg *plugin.Registry
	if registryHook != nil {
		reg = registryHook()
	} else {
		reg = plugin.NewRegistry()
		reg.Register(pacman.New())
		reg.Register(aur.New())
		reg.Register(flatpak.New())
		reg.Register(node.New())
	}

	return cfg, st, reg, u, nil
}

// registryHook is set by tests to inject a custom plugin registry.
var registryHook func() *plugin.Registry

func applyConfirmPrompt(hasInstallRemove, willUpgrade bool) string {
	var parts []string
	if willUpgrade {
		parts = append(parts, "run system upgrades")
	}
	if hasInstallRemove {
		parts = append(parts, "apply the planned install/remove changes")
	}
	if len(parts) == 0 {
		return "proceed?"
	}
	return "proceed to " + strings.Join(parts, ", then ") + "?"
}

// pluginsForUpgrade filters `all` to plugins not listed in `unavailable`
// (from planAll), preserving declaration order. Those plugins were already
// skipped during planning with a warning.
func pluginsForUpgrade(all []plugin.Plugin, unavailable []string) []plugin.Plugin {
	if len(unavailable) == 0 {
		return all
	}
	bad := toSet(unavailable)
	out := make([]plugin.Plugin, 0, len(all))
	for _, p := range all {
		if _, skip := bad[p.Name()]; skip {
			continue
		}
		out = append(out, p)
	}
	return out
}

// runUpgrades invokes Plugin.Upgrade for each plugin in order. Plugins that
// were unavailable during planning should already be filtered out; we still
// call Available as a cheap consistency check before Upgrade. If a plugin's
// upgrade fails, the error is recorded and the remaining plugins still run.
// A multi-error is returned if any upgrades failed.
func runUpgrades(plugins []plugin.Plugin, cfg *config.Config, st *state.State, r *runner.Runner, u *ui.UI) error {
	var errs []string
	for _, p := range plugins {
		if err := p.Available(cfg); err != nil {
			u.Warn("%s: unavailable (%v); skipping upgrade", p.Name(), err)
			continue
		}
		if err := p.Upgrade(cfg, st, r, u); err != nil {
			u.Warn("%s upgrade failed: %v", p.Name(), err)
			errs = append(errs, fmt.Sprintf("%s: %v", p.Name(), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("upgrade failures: %s", strings.Join(errs, "; "))
	}
	return nil
}

// collectUpgrades probes each available plugin for pending upgrades and
// returns the results. Unavailable plugins are skipped with a warning.
func collectUpgrades(plugins []plugin.Plugin, cfg *config.Config, r *runner.Runner, u *ui.UI) []plugin.UpgradeReport {
	var reports []plugin.UpgradeReport
	for _, p := range plugins {
		if err := p.Available(cfg); err != nil {
			continue
		}
		report, err := p.PendingUpgrades(cfg, r)
		if err != nil {
			u.Warn("%s: could not check upgrades: %v", p.Name(), err)
			continue
		}
		reports = append(reports, report)
	}
	return reports
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

const bashCompletionScript = `# bigkis bash completion
_bigkis_complete() {
    local cur prev words cword
    _init_completion || return
    local subcommands="apply status check doctor import explain rollback completion help"
    local plugins="pacman aur flatpak node"

    if [ "$cword" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "$subcommands --version --help" -- "$cur") )
        return
    fi

    case "${words[1]}" in
        apply)
            COMPREPLY=( $(compgen -W "--dry-run --yes --quiet --json --only --skip --no-upgrade --no-rollback --no-lock --lock --select --config" -- "$cur") )
            ;;
        status)
            COMPREPLY=( $(compgen -W "--json --exit-on-drift --only --skip --upgrades --config" -- "$cur") )
            ;;
        doctor)
            COMPREPLY=( $(compgen -W "--json --config" -- "$cur") )
            ;;
        import)
            COMPREPLY=( $(compgen -W "--output --only --aur-helper --node-manager --interactive" -- "$cur") )
            ;;
        rollback)
            COMPREPLY=( $(compgen -W "--list --latest --id --yes" -- "$cur") )
            ;;
        completion)
            COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
            ;;
    esac
}
complete -F _bigkis_complete bigkis
`

const zshCompletionScript = `#compdef bigkis
_bigkis() {
  local -a subcommands
  subcommands=(
    'apply:converge the system'
    'status:show drift'
    'check:validate config'
    'doctor:run preflight checks'
    'import:scan system into a starter system.toml'
    'explain:explain one package'
    'rollback:list or run a rollback script'
    'completion:print shell completion'
  )
  if (( CURRENT == 2 )); then
    _describe 'subcommand' subcommands
    return
  fi
  case "$words[2]" in
    apply)      _arguments '--dry-run' '--yes' '--quiet' '--json' '--only=' '--skip=' '--no-upgrade' '--no-rollback' '--no-lock' '--lock=' '--select' '--config=' ;;
    status)     _arguments '--json' '--exit-on-drift' '--only=' '--skip=' '--upgrades' '--config=' ;;
    doctor)     _arguments '--json' '--config=' ;;
    import)     _arguments '--output=' '--only=' '--aur-helper=' '--node-manager=' '--interactive' ;;
    rollback)   _arguments '--list' '--latest' '--id=' '--yes' ;;
    completion) _values 'shell' bash zsh fish ;;
  esac
}
_bigkis "$@"
`

const fishCompletionScript = `# bigkis fish completion
complete -c bigkis -n '__fish_use_subcommand' -a apply      -d 'converge the system'
complete -c bigkis -n '__fish_use_subcommand' -a status     -d 'show drift'
complete -c bigkis -n '__fish_use_subcommand' -a check      -d 'validate config'
complete -c bigkis -n '__fish_use_subcommand' -a doctor     -d 'run preflight checks'
complete -c bigkis -n '__fish_use_subcommand' -a import     -d 'scan system into a starter system.toml'
complete -c bigkis -n '__fish_use_subcommand' -a explain    -d 'explain one package'
complete -c bigkis -n '__fish_use_subcommand' -a rollback   -d 'list or run a rollback script'
complete -c bigkis -n '__fish_use_subcommand' -a completion -d 'print shell completion'

complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l dry-run     -d 'preview only'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l yes         -d 'skip confirmation'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l quiet       -d 'suppress info logs'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l json        -d 'emit JSON plan, do not apply'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l only        -d 'plugins to include'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l skip        -d 'plugins to skip'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l no-upgrade  -d 'skip system upgrades'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l no-rollback -d 'do not write rollback'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l no-lock     -d 'do not write lockfile'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l lock        -d 'lockfile path'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l select      -d 'interactive per-operation checkboxes'

complete -c bigkis -n '__fish_seen_subcommand_from status' -l json          -d 'JSON output'
complete -c bigkis -n '__fish_seen_subcommand_from status' -l exit-on-drift -d 'exit 3 on drift'
complete -c bigkis -n '__fish_seen_subcommand_from status' -l only          -d 'plugins to include'
complete -c bigkis -n '__fish_seen_subcommand_from status' -l skip          -d 'plugins to skip'
complete -c bigkis -n '__fish_seen_subcommand_from status' -l upgrades      -d 'show pending upgrades'

complete -c bigkis -n '__fish_seen_subcommand_from doctor' -l json          -d 'JSON output'

complete -c bigkis -n '__fish_seen_subcommand_from import' -l output       -d 'write to file'
complete -c bigkis -n '__fish_seen_subcommand_from import' -l only         -d 'plugins to include'
complete -c bigkis -n '__fish_seen_subcommand_from import' -l interactive  -d 'interactive package picker'

complete -c bigkis -n '__fish_seen_subcommand_from rollback' -l list   -d 'list rollback scripts'
complete -c bigkis -n '__fish_seen_subcommand_from rollback' -l latest -d 'run the most recent rollback'
complete -c bigkis -n '__fish_seen_subcommand_from rollback' -l id     -d 'run a specific rollback by id'
complete -c bigkis -n '__fish_seen_subcommand_from rollback' -l yes    -d 'skip confirmation'

complete -c bigkis -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
`
