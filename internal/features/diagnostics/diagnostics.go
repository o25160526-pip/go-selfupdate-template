// Package diagnostics is the reference feature: copy this shape for new ones.
//
// It is also genuinely useful. "Updates do not work on my machine" is almost
// always a path, permission, proxy or clock problem, and this prints all four.
package diagnostics

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"text/tabwriter"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/features"
	"github.com/o25160526-pip/go-selfupdate-template/internal/updater"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

func init() { features.Register(&Feature{}) }

type Feature struct{}

func (f *Feature) ID() string      { return "doctor" }
func (f *Feature) Summary() string { return "print environment, paths and update readiness" }

func (f *Feature) MenuItems() []features.MenuItem {
	return []features.MenuItem{{
		Label:   "Run diagnostics",
		Tooltip: "Check paths, permissions and signing keys",
		Run:     func(ctx context.Context) error { return f.Run(ctx, nil) },
	}}
}

func (f *Feature) Run(ctx context.Context, args []string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	defer w.Flush()

	row := func(k string, v any) { fmt.Fprintf(w, "%s\t%v\n", k, v) }

	row("version", version.Current)
	row("commit", version.Commit)
	row("built at", version.BuildDate)
	row("build channel", version.Channel)
	row("platform", runtime.GOOS+"/"+runtime.GOARCH)
	row("go runtime", runtime.Version())
	row("clock (UTC)", time.Now().UTC().Format(time.RFC3339))

	if exe, err := updater.SelfExe(); err != nil {
		row("executable", "unknown: "+err.Error())
	} else {
		row("executable", exe)
	}
	row("config file", config.FilePath())
	row("cache dir", config.CacheDir())
	row("state dir", config.StateDir())

	if backup, ok := updater.BackupPath(); ok {
		row("rollback available", backup)
	} else {
		row("rollback available", "no")
	}

	keys, kerr := updater.TrustedKeys()
	switch {
	case kerr != nil:
		row("signing keys", "invalid: "+kerr.Error())
	case len(keys) == 0:
		row("signing keys", "NONE - run `make keygen` and rebuild with PUBKEY=...")
	default:
		row("signing keys", fmt.Sprintf("%d trusted, rotation slot %s", len(keys), rotationState()))
	}

	if config.UpdateToken() != "" {
		row("update token", "present, internal channel available")
	} else {
		row("update token", "absent, stable and beta only")
	}
	for _, env := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if v := os.Getenv(env); v != "" {
			row(env, v)
		}
	}

	cfg, cerr := config.Load()
	if cerr != nil {
		row("config", "INVALID: "+cerr.Error())
		return nil
	}
	row("channel", cfg.Channel)
	row("auto update", cfg.AutoUpdate)
	row("manifest url", orNone(cfg.ManifestURL))
	for _, s := range cfg.Sources {
		state := "enabled"
		if !s.Enabled() {
			state = "disabled"
		}
		row("source "+s.Type, state)
	}
	return nil
}

func rotationState() string {
	if updater.PublicKeyNext != "" {
		return "filled"
	}
	return "EMPTY - losing the primary key would brick every client"
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
