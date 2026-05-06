package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/urfave/cli/v2"

	"codeberg.org/gurg/bigkis/internal/config"
	"codeberg.org/gurg/bigkis/internal/explain"
	"codeberg.org/gurg/bigkis/internal/importer"
	"codeberg.org/gurg/bigkis/internal/lockfile"
	"codeberg.org/gurg/bigkis/internal/plugin"
	"codeberg.org/gurg/bigkis/internal/plugin/aur"
	"codeberg.org/gurg/bigkis/internal/plugin/flatpak"
	"codeberg.org/gurg/bigkis/internal/plugin/node"
	"codeberg.org/gurg/bigkis/internal/plugin/pacman"
	"codeberg.org/gurg/bigkis/internal/rollback"
	"codeberg.org/gurg/bigkis/internal/runner"
	"codeberg.org/gurg/bigkis/internal/state"
	"codeberg.org/gurg/bigkis/internal/ui"
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
			&cli.BoolFlag{Name: "exit-on-drift", Usage: "exit with code 3 instead of 0 when drift is detected"},
		},
		Action: runStatus,
	}
}

func checkCommand() *cli.Command {
	return &cli.Command{
		Name:  "check",
		Usage: "validate the config (parse, schema, includes, host overlay, groups). No state, no system access.",
		Action: func(c *cli.Context) error {
			cfg, err := config.Load(c.String("config"))
			if err != nil {
				return err
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

func importCommand() *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "scan the current system and emit a starter system.toml on stdout",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "write to a file instead of stdout"},
			&cli.StringSliceFlag{Name: "only", Usage: "only import these plugins (comma-separated)"},
			&cli.StringFlag{Name: "aur-helper", Value: "yay", Usage: "value to write for [settings].aur_helper"},
			&cli.StringFlag{Name: "node-manager", Value: "npm", Usage: "value to write for [settings].node_manager"},
		},
		Action: func(c *cli.Context) error {
			out := os.Stdout
			if path := c.String("output"); path != "" {
				f, err := os.Create(path)
				if err != nil {
					return err
				}
				defer f.Close()
				out = f
			}
			return importer.Run(out, importer.Options{
				Only:        splitCSV(c.StringSlice("only")),
				AURHelper:   c.String("aur-helper"),
				NodeManager: c.String("node-manager"),
			})
		},
	}
}

func explainCommand() *cli.Command {
	return &cli.Command{
		Name:      "explain",
		Usage:     "explain a single package: declared, installed, managed, status",
		ArgsUsage: "<package>",
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return fmt.Errorf("usage: bigkis explain <package>")
			}
			cfg, st, _, _, err := bootstrap(c)
			if err != nil {
				return err
			}
			r := explain.Inspect(c.Args().First(), cfg, st)
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
	plugins := selectPlugins(cfg.Settings.Enabled, nil, nil, reg, u)
	hasChanges := false
	for _, p := range plugins {
		if err := p.Available(cfg); err != nil {
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
	}
	type reportJSON struct {
		ConfigPath string       `json:"configPath"`
		InSync     bool         `json:"inSync"`
		Plugins    []pluginJSON `json:"plugins"`
	}

	out := reportJSON{ConfigPath: cfg.Path, InSync: true}

	plugins := selectPlugins(cfg.Settings.Enabled, nil, nil, reg, ui.New(os.Stderr, os.Stdin, false, true))
	for _, p := range plugins {
		pj := pluginJSON{Name: p.Name(), Available: true, InSync: true}
		if err := p.Available(cfg); err != nil {
			pj.Available = false
			pj.AvailableEr = err.Error()
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
	if c.Bool("quiet") {
		u.SetQuiet(true)
	}
	only := splitCSV(c.StringSlice("only"))
	skip := splitCSV(c.StringSlice("skip"))

	plugins := selectPlugins(cfg.Settings.Enabled, only, skip, reg, u)
	if len(plugins) == 0 {
		u.Warn("no plugins selected; nothing to do")
		return nil
	}

	stages, overallChanges, err := planAll(cfg, st, plugins, u)
	if err != nil {
		return err
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
		return cli.Exit("aborted by user", ExitUserCancelled)
	}

	if !c.Bool("no-rollback") {
		var ops []rollback.Op
		for _, s := range stages {
			ops = append(ops, rollback.OpsForReport(s.Plugin.Name(), cfg, s.Report)...)
		}
		if path, err := rollback.Write(ops); err != nil {
			u.Warn("could not write rollback script: %v", err)
		} else if path != "" {
			u.Dim("rollback script: %s", path)
		}
	}

	r := runner.New(false)
	u.Info("applying")
	statePath := state.DefaultPath()
	if err := applyStages(stages, cfg, st, statePath, r, u); err != nil {
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

// planAll runs Plan for each plugin and returns the stages with changes,
// plus whether anything overall has changes. Plugins whose Available()
// returns an error are skipped with a warning.
func planAll(cfg *config.Config, st *state.State, plugins []plugin.Plugin, u *ui.UI) ([]stage, bool, error) {
	var stages []stage
	overall := false
	u.Info("planning")
	for _, p := range plugins {
		if err := p.Available(cfg); err != nil {
			u.Warn("%s: unavailable (%v); skipping", p.Name(), err)
			continue
		}
		report, err := p.Plan(cfg, st)
		if err != nil {
			return nil, false, fmt.Errorf("%s plan: %w", p.Name(), err)
		}
		if !report.HasChanges() {
			u.Step("%s: in sync", p.Name())
			continue
		}
		overall = true
		u.Step("%s: %d change(s)", p.Name(), len(report.Operations))
		printReport(u, report)
		stages = append(stages, stage{Plugin: p, Report: report})
	}
	return stages, overall, nil
}

// applyStages runs each stage's Apply + PersistState and checkpoints the
// state file after every successful plugin. This way a failure mid-loop
// leaves state.json describing what was actually applied, not a stale
// snapshot from before the run started.
func applyStages(stages []stage, cfg *config.Config, st *state.State, statePath string, r *runner.Runner, u *ui.UI) error {
	for _, s := range stages {
		u.Info("plugin: %s", s.Plugin.Name())
		if err := s.Plugin.Apply(cfg, st, s.Report, r, u); err != nil {
			return fmt.Errorf("%s apply: %w", s.Plugin.Name(), err)
		}
		if err := s.Plugin.PersistState(cfg, st); err != nil {
			return fmt.Errorf("%s persist state: %w", s.Plugin.Name(), err)
		}
		if statePath != "" {
			if err := state.Save(statePath, st); err != nil {
				return fmt.Errorf("checkpoint state after %s: %w", s.Plugin.Name(), err)
			}
		}
	}
	return nil
}

// runApplyJSON emits a machine-readable plan to stdout. It does NOT actually
// apply changes; the JSON mode is intended for tooling that wants the same
// shape as `status --json` plus an explicit dryRun flag. Combine with
// --no-rollback / --no-lock to script around bigkis.
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
	}
	type reportJSON struct {
		ConfigPath string       `json:"configPath"`
		DryRun     bool         `json:"dryRun"`
		InSync     bool         `json:"inSync"`
		Plugins    []pluginJSON `json:"plugins"`
	}

	out := reportJSON{ConfigPath: cfg.Path, DryRun: true, InSync: true}
	only := splitCSV(c.StringSlice("only"))
	skip := splitCSV(c.StringSlice("skip"))
	plugins := selectPlugins(cfg.Settings.Enabled, only, skip, reg, logUI)
	for _, p := range plugins {
		pj := pluginJSON{Name: p.Name(), Available: true, InSync: true}
		if err := p.Available(cfg); err != nil {
			pj.Available = false
			pj.AvailableEr = err.Error()
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
		out.Plugins = append(out.Plugins, pj)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// bootstrap loads config + state and constructs the plugin registry.
func bootstrap(c *cli.Context) (*config.Config, *state.State, *plugin.Registry, *ui.UI, error) {
	return bootstrapTo(c, os.Stdout)
}

// bootstrapTo is bootstrap, but lets the caller route UI output to a specific
// writer (useful so JSON output on stdout is not polluted by logs).
func bootstrapTo(c *cli.Context, logTo *os.File) (*config.Config, *state.State, *plugin.Registry, *ui.UI, error) {
	u := ui.New(logTo, os.Stdin, ui.IsColorTTY(logTo), false)

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

const bashCompletionScript = `# bigkis bash completion
_bigkis_complete() {
    local cur prev words cword
    _init_completion || return
    local subcommands="apply status check import explain rollback completion help"
    local plugins="pacman aur flatpak node"

    if [ "$cword" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "$subcommands --version --help" -- "$cur") )
        return
    fi

    case "${words[1]}" in
        apply)
            COMPREPLY=( $(compgen -W "--dry-run --yes --only --skip --no-rollback --no-lock --lock --config" -- "$cur") )
            ;;
        status)
            COMPREPLY=( $(compgen -W "--json --config" -- "$cur") )
            ;;
        import)
            COMPREPLY=( $(compgen -W "--output --only --aur-helper --node-manager" -- "$cur") )
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
    apply)      _arguments '--dry-run' '--yes' '--only=' '--skip=' '--no-rollback' '--no-lock' '--lock=' '--config=' ;;
    status)     _arguments '--json' '--config=' ;;
    import)     _arguments '--output=' '--only=' '--aur-helper=' '--node-manager=' ;;
    completion) _values 'shell' bash zsh fish ;;
  esac
}
_bigkis "$@"
`

const fishCompletionScript = `# bigkis fish completion
complete -c bigkis -n '__fish_use_subcommand' -a apply      -d 'converge the system'
complete -c bigkis -n '__fish_use_subcommand' -a status     -d 'show drift'
complete -c bigkis -n '__fish_use_subcommand' -a check      -d 'validate config'
complete -c bigkis -n '__fish_use_subcommand' -a import     -d 'scan system into a starter system.toml'
complete -c bigkis -n '__fish_use_subcommand' -a explain    -d 'explain one package'
complete -c bigkis -n '__fish_use_subcommand' -a rollback   -d 'list or run a rollback script'
complete -c bigkis -n '__fish_use_subcommand' -a completion -d 'print shell completion'

complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l dry-run     -d 'preview only'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l yes         -d 'skip confirmation'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l only        -d 'plugins to include'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l skip        -d 'plugins to skip'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l no-rollback -d 'do not write rollback'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l no-lock     -d 'do not write lockfile'
complete -c bigkis -n '__fish_seen_subcommand_from apply'  -l lock        -d 'lockfile path'

complete -c bigkis -n '__fish_seen_subcommand_from status' -l json        -d 'JSON output'
complete -c bigkis -n '__fish_seen_subcommand_from import' -l output      -d 'write to file'
complete -c bigkis -n '__fish_seen_subcommand_from import' -l only        -d 'plugins to include'

complete -c bigkis -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
`
