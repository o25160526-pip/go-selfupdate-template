// Package ui is the interactive update menu.
//
// Plain stdin and stdout on purpose: it works over SSH, inside a container and
// in a Windows console, and it adds no dependencies. The tray calls the same
// updater methods, so the two surfaces cannot drift apart.
package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/features"
	"github.com/o25160526-pip/go-selfupdate-template/internal/updater"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

type Menu struct {
	U   *updater.Updater
	Cfg *config.Config
	In  *bufio.Reader
	Out io.Writer
}

func NewMenu(u *updater.Updater, cfg *config.Config) *Menu {
	return &Menu{U: u, Cfg: cfg, In: bufio.NewReader(os.Stdin), Out: os.Stdout}
}

func (m *Menu) Run(ctx context.Context) error {
	for {
		m.header()
		fmt.Fprintln(m.Out, "  1  Check for updates")
		fmt.Fprintln(m.Out, "  2  Choose a version to install")
		fmt.Fprintln(m.Out, "  3  Update to the newest version")
		fmt.Fprintln(m.Out, "  4  Roll back to the previous version")
		fmt.Fprintln(m.Out, "  5  Switch channel")
		fmt.Fprintln(m.Out, "  6  Cache: list, prefetch, prune")
		fmt.Fprintf(m.Out, "  7  Toggle automatic updates (currently %s)\n", onOff(m.Cfg.AutoUpdate))
		fmt.Fprintln(m.Out, "  8  Snooze reminders for 24 hours")

		extras := features.MenuItems()
		for i, item := range extras {
			fmt.Fprintf(m.Out, "  %d  %s\n", 9+i, item.Label)
		}
		fmt.Fprintln(m.Out, "  0  Quit")

		choice, err := m.prompt("\nSelect: ")
		if err != nil {
			return err
		}
		var actErr error
		switch choice {
		case "0", "q", "quit", "exit":
			return nil
		case "1":
			actErr = m.check(ctx)
		case "2":
			actErr = m.chooseVersion(ctx)
		case "3":
			actErr = m.update(ctx, updater.Options{})
		case "4":
			actErr = m.rollback()
		case "5":
			actErr = m.switchChannel()
		case "6":
			actErr = m.cache(ctx)
		case "7":
			m.Cfg.AutoUpdate = !m.Cfg.AutoUpdate
			actErr = m.Cfg.Save()
		case "8":
			m.Cfg.SnoozeUntil = time.Now().Add(24 * time.Hour)
			if actErr = m.Cfg.Save(); actErr == nil {
				fmt.Fprintf(m.Out, "Snoozed until %s.\n", m.Cfg.SnoozeUntil.Format(time.RFC1123))
			}
		default:
			if n, cerr := strconv.Atoi(choice); cerr == nil && n >= 9 && n < 9+len(extras) {
				actErr = extras[n-9].Run(ctx)
			} else {
				fmt.Fprintln(m.Out, "Not a valid choice.")
			}
		}
		if actErr != nil {
			fmt.Fprintf(m.Out, "\n!! %v\n", actErr)
		}
		fmt.Fprintln(m.Out)
	}
}

func (m *Menu) header() {
	fmt.Fprintf(m.Out, "\n%s %s  [channel: %s]\n", m.Cfg.AppName, version.Current, m.Cfg.Channel)
	if m.Cfg.Snoozed() {
		fmt.Fprintf(m.Out, "reminders snoozed until %s\n", m.Cfg.SnoozeUntil.Format(time.RFC1123))
	}
	fmt.Fprintln(m.Out, strings.Repeat("-", 52))
}

func (m *Menu) prompt(label string) (string, error) {
	fmt.Fprint(m.Out, label)
	line, err := m.In.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			// Piped input ran out: quit instead of spinning forever.
			return "0", nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (m *Menu) check(ctx context.Context) error {
	plan, err := m.U.Plan(ctx, updater.Options{})
	if err != nil {
		return err
	}
	if plan.UpToDate {
		fmt.Fprintf(m.Out, "\nUp to date (%s). %s\n", plan.Current, plan.Reason)
		return nil
	}
	fmt.Fprintf(m.Out, "\n%s is available from %s", plan.Target.Version, strings.Join(plan.Target.SourceNames(), ", "))
	if plan.Mandatory {
		fmt.Fprint(m.Out, "  [MANDATORY]")
	}
	fmt.Fprintln(m.Out)
	if notes := strings.TrimSpace(plan.Target.Notes); notes != "" {
		fmt.Fprintf(m.Out, "\n%s\n", truncate(notes, 600))
	}
	return nil
}

// chooseVersion implements the "client picks which version to install"
// requirement. Older versions are listed too, but installing one is an
// explicit, confirmed decision.
func (m *Menu) chooseVersion(ctx context.Context) error {
	cands, err := m.U.Available(ctx, m.Cfg.Channel)
	if err != nil {
		return err
	}
	if len(cands) == 0 {
		fmt.Fprintln(m.Out, "No versions found.")
		return nil
	}
	cached := m.cachedVersions()

	w := tabwriter.NewWriter(m.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "\n  #\tVERSION\tSOURCES\tCACHED\tPUBLISHED\t")
	for i, c := range cands {
		mark := ""
		if c.Version.Equal(m.U.Current) {
			mark = " (installed)"
		}
		pub := "-"
		if !c.PublishedAt.IsZero() {
			pub = c.PublishedAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "  %d\t%s%s\t%s\t%s\t%s\t\n", i+1, c.Version, mark,
			strings.Join(c.SourceNames(), ","), yesNo(cached[c.Version.String()]), pub)
	}
	w.Flush()

	ans, err := m.prompt("\nVersion number to install (empty to cancel): ")
	if err != nil || ans == "" {
		return err
	}
	n, cerr := strconv.Atoi(ans)
	if cerr != nil || n < 1 || n > len(cands) {
		return fmt.Errorf("%q is not in the list", ans)
	}
	target := cands[n-1]
	opt := updater.Options{TargetVersion: target.Version.String()}
	if target.Version.Before(m.U.Current) {
		ok, perr := m.confirm(fmt.Sprintf("%s is OLDER than the installed %s. Downgrade anyway?", target.Version, m.U.Current))
		if perr != nil || !ok {
			return perr
		}
		opt.AllowDowngrade = true
	}
	return m.update(ctx, opt)
}

func (m *Menu) update(ctx context.Context, opt updater.Options) error {
	last := -1
	opt.Progress = func(done, total int64) {
		if total <= 0 {
			return
		}
		if pct := int(done * 100 / total); pct != last && pct%5 == 0 {
			last = pct
			fmt.Fprintf(m.Out, "\rdownloading %3d%%", pct)
		}
	}
	res, err := m.U.Update(ctx, opt)
	fmt.Fprintln(m.Out)
	if err != nil {
		return err
	}
	switch {
	case res.UpToDate:
		fmt.Fprintf(m.Out, "Nothing to do: %s\n", res.Reason)
	case res.Applied:
		fmt.Fprintf(m.Out, "Updated %s -> %s from %s. Restart to run the new version.\n", res.From, res.To, res.Source)
	default:
		fmt.Fprintf(m.Out, "Resolved %s from %s but nothing was installed.\n", res.To, res.Source)
	}
	return nil
}

func (m *Menu) rollback() error {
	if _, ok := updater.BackupPath(); !ok {
		fmt.Fprintln(m.Out, "No previous version is stored next to the executable.")
		return nil
	}
	ok, err := m.confirm("Restore the previous binary?")
	if err != nil || !ok {
		return err
	}
	got, err := m.U.RollbackToBackup()
	if err != nil {
		return err
	}
	fmt.Fprintf(m.Out, "Rolled back to %s. Restart to use it.\n", got)
	return nil
}

func (m *Menu) switchChannel() error {
	fmt.Fprintln(m.Out, "\n  1  stable")
	fmt.Fprintln(m.Out, "  2  beta")
	fmt.Fprintln(m.Out, "  3  internal (needs APP_UPDATE_TOKEN)")
	ans, err := m.prompt("Channel: ")
	if err != nil {
		return err
	}
	switch ans {
	case "1":
		m.Cfg.Channel = config.ChannelStable
	case "2":
		m.Cfg.Channel = config.ChannelBeta
	case "3":
		if config.UpdateToken() == "" {
			return fmt.Errorf("the internal channel needs APP_UPDATE_TOKEN in the environment")
		}
		m.Cfg.Channel = config.ChannelInternal
	default:
		return nil
	}
	if err := m.Cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(m.Out, "Channel is now %s.\n", m.Cfg.Channel)
	return nil
}

func (m *Menu) cache(ctx context.Context) error {
	blobs, err := m.U.Cache.Blobs()
	if err != nil {
		return err
	}
	fmt.Fprintf(m.Out, "\n%d version(s) cached in %s\n", len(blobs), m.U.Cache.Dir)
	for _, b := range blobs {
		fmt.Fprintf(m.Out, "  %s  %s  %.1f MB\n", b.Version, b.Source, float64(b.Size)/(1<<20))
	}
	fmt.Fprintln(m.Out, "\n  1  Prefetch newer versions")
	fmt.Fprintln(m.Out, "  2  Prune to the configured limit")
	fmt.Fprintln(m.Out, "  3  Clear everything")
	ans, err := m.prompt("Action (empty to cancel): ")
	if err != nil {
		return err
	}
	switch ans {
	case "1":
		n, perr := m.U.Prefetch(ctx, m.Cfg.Channel, m.Cfg.PrefetchCount)
		if perr != nil {
			return perr
		}
		fmt.Fprintf(m.Out, "%d version(s) ready locally.\n", n)
	case "2":
		n, perr := m.U.Cache.Prune(m.Cfg.KeepBlobs)
		if perr != nil {
			return perr
		}
		fmt.Fprintf(m.Out, "Removed %d file(s).\n", n)
	case "3":
		if err := m.U.Cache.Clear(); err != nil {
			return err
		}
		fmt.Fprintln(m.Out, "Cache cleared.")
	}
	return nil
}

func (m *Menu) cachedVersions() map[string]bool {
	out := map[string]bool{}
	blobs, err := m.U.Cache.Blobs()
	if err != nil {
		return out
	}
	for _, b := range blobs {
		out[b.Version.String()] = true
	}
	return out
}

func (m *Menu) confirm(question string) (bool, error) {
	ans, err := m.prompt(question + " [y/N]: ")
	if err != nil {
		return false, err
	}
	ans = strings.ToLower(ans)
	return ans == "y" || ans == "yes", nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
