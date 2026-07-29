// Package cli is the command surface.
//
// Hand-rolled dispatch on the stdlib flag package keeps the module dependency
// free, which is what lets all six targets cross-compile from a single runner
// with CGO_ENABLED=0 and no vendoring.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/exitcode"
	"github.com/o25160526-pip/go-selfupdate-template/internal/features"
	"github.com/o25160526-pip/go-selfupdate-template/internal/tray"
	"github.com/o25160526-pip/go-selfupdate-template/internal/ui"
	"github.com/o25160526-pip/go-selfupdate-template/internal/updater"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// Run executes a command and returns the process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		usage(os.Stdout)
		return exitcode.Usage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version":
		return cmdVersion(rest)
	case "update":
		return cmdUpdate(ctx, rest)
	case "rollback":
		return cmdRollback(rest)
	case "channel":
		return cmdChannel(rest)
	case "cache":
		return cmdCache(ctx, rest)
	case "config":
		return cmdConfig(rest)
	case "menu":
		return cmdMenu(ctx)
	case "tray":
		return cmdTray(ctx)
	case "help", "-h", "--help":
		usage(os.Stdout)
		return 0
	default:
		// Unknown verbs fall through to the feature registry, so adding a
		// feature needs no CLI wiring at all.
		if f, ok := features.Lookup(cmd); ok {
			if err := f.Run(ctx, rest); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", cmd, err)
				return exitcode.From(err)
			}
			return 0
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return exitcode.Usage
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "%s %s\n\nUsage: %s <command> [flags]\n\nCommands:\n", config.AppName, version.Current, config.AppName)
	fmt.Fprintln(w, "  update              install a newer, or a specific, version")
	fmt.Fprintln(w, "  rollback            restore the previous binary")
	fmt.Fprintln(w, "  menu                interactive update menu")
	fmt.Fprintln(w, "  tray                run in the system tray")
	fmt.Fprintln(w, "  channel             show or set the update channel")
	fmt.Fprintln(w, "  cache               inspect, prefetch or prune cached versions")
	fmt.Fprintln(w, "  config              show the effective configuration")
	fmt.Fprintln(w, "  version             print version information")

	if extras := features.All(); len(extras) > 0 {
		fmt.Fprintln(w, "\nFeatures:")
		for _, f := range extras {
			fmt.Fprintf(w, "  %-18s %s\n", f.ID(), f.Summary())
		}
	}
	fmt.Fprintln(w, "\nExit codes of `update --silent`:")
	fmt.Fprintln(w, "   0 updated              10 already newest      20 version not found")
	fmt.Fprintln(w, "  30 verification failed  40 apply failed        50 sources unreachable")
	fmt.Fprintln(w, "  60 another update running")
	fmt.Fprintf(w, "\nRun \"%s update -h\" for update flags.\n", config.AppName)
}

func cmdVersion(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	short := fs.Bool("short", false, "print only the version string")
	asJSON := fs.Bool("json", false, "print machine readable output")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	// `version --short` is a contract: the CI smoke test and the post-apply
	// self-check both parse this exact output. Do not decorate it.
	if *short {
		fmt.Println(version.Current)
		return 0
	}
	info := version.Info{
		Version:   version.Current,
		Commit:    version.Commit,
		BuildDate: version.BuildDate,
		Channel:   version.Channel,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
	if v, err := version.Parse(version.Current); err == nil {
		info.Semver = v.Semver()
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(info); err != nil {
			return 1
		}
		return 0
	}
	fmt.Printf("%s %s (%s) built %s on %s/%s [%s]\n",
		config.AppName, info.Version, info.Commit, info.BuildDate, info.OS, info.Arch, info.Channel)
	return 0
}

func cmdUpdate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	target := fs.String("version", "", "install this exact version (1.YY.MMDD.HHmm)")
	latest := fs.Bool("latest", false, "install the newest version on any source (this is the default)")
	silent := fs.Bool("silent", false, "no prompts and no progress output; use the documented exit codes")
	dryRun := fs.Bool("dry-run", false, "resolve and verify but do not install")
	channel := fs.String("channel", "", "stable | beta | internal")
	allowDown := fs.Bool("allow-downgrade", false, "permit installing an older version")
	force := fs.Bool("force", false, "ignore rollout, pause, snooze and the failed-version guard")
	timeout := fs.Duration("timeout", 0, "give up after this long (default: the configured timeout)")
	list := fs.Bool("list", false, "list available versions and exit")
	prefetch := fs.Int("prefetch", -1, "download the N newest versions without installing anything")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	if *target != "" && *latest {
		fmt.Fprintln(os.Stderr, "--version and --latest are mutually exclusive")
		return exitcode.Usage
	}

	cfg, u, code := setup(*channel, *silent)
	if code != 0 {
		return code
	}

	if *list {
		return listVersions(ctx, u, cfg)
	}
	if *prefetch >= 0 {
		n, err := u.Prefetch(ctx, cfg.Channel, *prefetch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "prefetch: %v\n", err)
			return exitcode.From(err)
		}
		fmt.Printf("%d version(s) ready locally\n", n)
		return 0
	}

	opt := updater.Options{
		Channel:        cfg.Channel,
		TargetVersion:  *target,
		Silent:         *silent,
		DryRun:         *dryRun,
		AllowDowngrade: *allowDown,
		Force:          *force,
		Timeout:        *timeout,
	}
	if !*silent {
		last := -1
		opt.Progress = func(done, total int64) {
			if total <= 0 {
				return
			}
			if pct := int(done * 100 / total); pct != last && pct%5 == 0 {
				last = pct
				fmt.Fprintf(os.Stderr, "\rdownloading %3d%%", pct)
			}
		}
	}

	res, err := u.Update(ctx, opt)
	if !*silent {
		fmt.Fprintln(os.Stderr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		return exitcode.From(err)
	}
	switch {
	case res.UpToDate:
		fmt.Printf("already up to date (%s): %s\n", res.From, res.Reason)
		// Exit 10, not 0: CI must be able to tell "nothing to do" apart from
		// "an update was proven to work".
		return exitcode.UpToDate
	case res.DryRun:
		fmt.Printf("would update %s -> %s from %s (%s)\n", res.From, res.To, res.Source, res.Asset)
		return 0
	default:
		cached := ""
		if res.FromCach {
			cached = " (from the local cache)"
		}
		fmt.Printf("updated %s -> %s from %s%s\n", res.From, res.To, res.Source, cached)
		return 0
	}
}

func listVersions(ctx context.Context, u *updater.Updater, cfg *config.Config) int {
	if err := u.LoadManifest(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitcode.From(err)
	}
	cands, err := u.Available(ctx, cfg.Channel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitcode.From(err)
	}
	cached := map[string]bool{}
	if blobs, berr := u.Cache.Blobs(); berr == nil {
		for _, b := range blobs {
			cached[b.Version.String()] = true
		}
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tSOURCES\tCACHED\tSTATE\t")
	for _, c := range cands {
		state := ""
		switch {
		case c.Version.Equal(u.Current):
			state = "installed"
		case u.Manifest.IsBlocked(c.Version):
			state = "withdrawn"
		case c.Draft:
			state = "draft"
		case c.Prerelease:
			state = "prerelease"
		}
		mark := "no"
		if cached[c.Version.String()] {
			mark = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", c.Version, strings.Join(c.SourceNames(), ","), mark, state)
	}
	w.Flush()
	return 0
}

func cmdRollback(args []string) int {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return exitcode.Usage
	}
	_, u, code := setup("", true)
	if code != 0 {
		return code
	}
	backup, ok := updater.BackupPath()
	if !ok {
		fmt.Fprintln(os.Stderr, "no previous binary is stored next to the executable")
		return exitcode.NotFound
	}
	if !*yes {
		fmt.Printf("Restore the previous binary from %s? [y/N] ", backup)
		var answer string
		fmt.Scanln(&answer)
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			return 0
		}
	}
	got, err := u.RollbackToBackup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rollback failed: %v\n", err)
		return exitcode.From(err)
	}
	fmt.Printf("rolled back to %s\n", got)
	return 0
}

func cmdChannel(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return exitcode.Usage
	}
	if len(args) == 0 || args[0] == "show" {
		fmt.Println(cfg.Channel)
		return 0
	}
	if args[0] != "set" || len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s channel [show|set <stable|beta|internal>]\n", config.AppName)
		return exitcode.Usage
	}
	if !config.ValidChannel(args[1]) {
		fmt.Fprintf(os.Stderr, "unknown channel %q\n", args[1])
		return exitcode.Usage
	}
	if args[1] == config.ChannelInternal && config.UpdateToken() == "" {
		fmt.Fprintln(os.Stderr, "the internal channel needs APP_UPDATE_TOKEN in the environment")
		return exitcode.Usage
	}
	cfg.Channel = args[1]
	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "save config: %v\n", err)
		return 1
	}
	fmt.Printf("channel set to %s\n", cfg.Channel)
	return 0
}

func cmdCache(ctx context.Context, args []string) int {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	cfg, u, code := setup("", true)
	if code != 0 {
		return code
	}
	switch sub {
	case "list":
		blobs, err := u.Cache.Blobs()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "VERSION\tSOURCE\tSIZE\tLAST USED\t")
		for _, b := range blobs {
			fmt.Fprintf(w, "%s\t%s\t%.1f MB\t%s\t\n", b.Version, b.Source,
				float64(b.Size)/(1<<20), b.LastUsed.Local().Format("2006-01-02 15:04"))
		}
		w.Flush()
		fmt.Printf("\n%d cached in %s\n", len(blobs), u.Cache.Dir)
	case "prefetch":
		n, err := u.Prefetch(ctx, cfg.Channel, cfg.PrefetchCount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return exitcode.From(err)
		}
		fmt.Printf("%d version(s) ready locally\n", n)
	case "prune":
		n, err := u.Cache.Prune(cfg.KeepBlobs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		fmt.Printf("removed %d file(s)\n", n)
	case "clear":
		if err := u.Cache.Clear(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		fmt.Println("cache cleared")
	default:
		fmt.Fprintf(os.Stderr, "usage: %s cache [list|prefetch|prune|clear]\n", config.AppName)
		return exitcode.Usage
	}
	return 0
}

func cmdConfig(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return exitcode.Usage
	}
	if len(args) > 0 && args[0] == "path" {
		fmt.Println(cfg.Path())
		return 0
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return 1
	}
	fmt.Fprintf(os.Stderr, "\n# effective configuration (flag > env > %s > manifest > default)\n", cfg.Path())
	return 0
}

func cmdMenu(ctx context.Context) int {
	cfg, u, code := setup("", false)
	if code != 0 {
		return code
	}
	if err := ui.NewMenu(u, cfg).Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitcode.From(err)
	}
	return 0
}

func cmdTray(ctx context.Context) int {
	cfg, u, code := setup("", false)
	if code != 0 {
		return code
	}
	ctrl := tray.New(cfg.AppName)
	if !ctrl.Supported() {
		fmt.Fprintf(os.Stderr, "no system tray in this build: rebuild with `-tags tray`, or run `%s menu`\n", config.AppName)
		return exitcode.Usage
	}
	items := []tray.Item{
		{Label: "Check now", OnClick: func() {
			plan, err := u.Plan(ctx, updater.Options{})
			switch {
			case err != nil:
				ctrl.SetStatus(tray.StatusError, err.Error())
			case plan.UpToDate:
				ctrl.SetStatus(tray.StatusOK, "up to date: "+plan.Reason)
			default:
				ctrl.SetStatus(tray.StatusUpdateAvailable, "available: "+plan.Target.Version.String())
				ctrl.Notify("Update available", plan.Target.Version.String())
			}
		}},
		{Label: "Update now", OnClick: func() {
			ctrl.SetStatus(tray.StatusWorking, "updating")
			res, err := u.Update(ctx, updater.Options{Silent: true})
			if err != nil {
				ctrl.SetStatus(tray.StatusError, err.Error())
				ctrl.Notify("Update failed", err.Error())
				return
			}
			ctrl.SetStatus(tray.StatusOK, res.To.String())
			if res.Applied {
				ctrl.Notify("Updated", fmt.Sprintf("Now on %s. Restart to apply.", res.To))
			}
		}},
		{Separator: true},
		{Label: "Snooze 24h", OnClick: func() {
			cfg.SnoozeUntil = time.Now().Add(24 * time.Hour)
			_ = cfg.Save()
		}},
	}
	// Features contribute their own tray entries without touching this list.
	for _, mi := range features.MenuItems() {
		item := mi
		items = append(items, tray.Item{Label: item.Label, Tooltip: item.Tooltip, OnClick: func() {
			if err := item.Run(ctx); err != nil {
				ctrl.SetStatus(tray.StatusError, err.Error())
			}
		}})
	}
	ctrl.SetItems(items)
	if err := ctrl.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
}

// setup loads configuration and builds the updater, applying flag precedence.
func setup(channel string, quiet bool) (*config.Config, *updater.Updater, int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return nil, nil, exitcode.Usage
	}
	if channel != "" {
		if !config.ValidChannel(channel) {
			fmt.Fprintf(os.Stderr, "unknown channel %q\n", channel)
			return nil, nil, exitcode.Usage
		}
		cfg.Channel = channel
	}
	level := slog.LevelInfo
	if quiet {
		level = slog.LevelWarn
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	u, err := updater.New(cfg, log)
	if err != nil {
		if errors.Is(err, updater.ErrNoTrustedKeys) {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return nil, nil, exitcode.VerifyError
		}
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return nil, nil, exitcode.Usage
	}
	return cfg, u, 0
}
