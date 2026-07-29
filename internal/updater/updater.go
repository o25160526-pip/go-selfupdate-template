package updater

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/exitcode"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// Updater orchestrates check, resolve, download, verify and apply.
type Updater struct {
	Cfg      *config.Config
	Sources  []Source
	Cache    *Cache
	Keys     []ed25519.PublicKey
	HTTP     *http.Client
	Log      *slog.Logger
	Current  version.Version
	Manifest *Manifest
	State    *State

	mu      sync.Mutex
	latency map[string]time.Duration
}

// Options are the knobs of a single update run.
type Options struct {
	Channel        string
	TargetVersion  string // exact version; empty means "newest"
	Silent         bool
	DryRun         bool
	AllowDowngrade bool
	Force          bool // ignore rollout, pause, snooze and the failed-version guard
	Timeout        time.Duration
	Progress       Progress
}

// Result describes what an update run did.
type Result struct {
	From     version.Version
	To       version.Version
	Source   string
	Asset    string
	UpToDate bool
	Reason   string
	Applied  bool
	DryRun   bool
	Backup   string
	FromCach bool
}

// New builds an Updater from configuration.
func New(cfg *config.Config, log *slog.Logger) (*Updater, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	keys, err := TrustedKeys()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 && cfg.RequireSignature && !cfg.InsecureSkipVerify {
		return nil, ErrNoTrustedKeys
	}
	cur, err := version.Self()
	if err != nil {
		return nil, fmt.Errorf("this build reports an invalid version %q: %w", version.Current, err)
	}
	client := NewHTTPClient(cfg.Timeout.Duration())
	srcs, err := NewSources(cfg, client)
	if err != nil {
		return nil, err
	}
	return &Updater{
		Cfg:     cfg,
		Sources: srcs,
		Cache:   NewCache(config.CacheDir(), cfg.CacheTTL.Duration(), cfg.KeepBlobs),
		Keys:    keys,
		HTTP:    client,
		Log:     log,
		Current: cur,
		State:   LoadState(config.StateDir()),
		latency: map[string]time.Duration{},
	}, nil
}

func (u *Updater) verifyEnabled() bool {
	return len(u.Keys) > 0 && !u.Cfg.InsecureSkipVerify
}

func (u *Updater) channel(opt Options) string {
	if opt.Channel != "" {
		return opt.Channel
	}
	return u.Cfg.Channel
}

// listOptions maps a channel onto what each source should return.
func (u *Updater) listOptions(channel string) (ListOptions, error) {
	opt := ListOptions{Limit: 50}
	switch channel {
	case config.ChannelStable:
	case config.ChannelBeta:
		opt.IncludePrerelease = true
	case config.ChannelInternal:
		// Draft releases require a token. Fail loudly rather than silently
		// falling back to stable: a CI smoke test that quietly checked the
		// wrong channel would pass while proving nothing.
		if config.UpdateToken() == "" {
			return opt, fmt.Errorf("channel %q needs APP_UPDATE_TOKEN in the environment (never embedded in a released binary)", channel)
		}
		opt.IncludeDraft = true
		opt.IncludePrerelease = true
	default:
		return opt, fmt.Errorf("unknown channel %q", channel)
	}
	return opt, nil
}

// LoadManifest fetches the signed policy. Network failures are tolerated so an
// offline client can still update from cache; a bad signature is never
// tolerated, because that is the one failure that might be an attack.
func (u *Updater) LoadManifest(ctx context.Context) error {
	if u.Cfg.ManifestURL == "" {
		return nil
	}
	f := &ManifestFetcher{HTTP: u.HTTP, Keys: u.Keys, SkipVerify: !u.verifyEnabled()}
	m, err := f.Fetch(ctx, u.Cfg.ManifestURL)
	if err != nil {
		if errors.Is(err, ErrSignature) || errors.Is(err, ErrNoTrustedKeys) {
			return exitcode.Wrap(exitcode.VerifyError, err)
		}
		u.Log.Warn("policy manifest unavailable, continuing without it", "err", err)
		return nil
	}
	u.Manifest = m
	return nil
}

// Available lists every version offered by any source, newest first.
//
// Sources are probed in parallel, and one dead source does not fail the run:
// that is the whole point of having two.
func (u *Updater) Available(ctx context.Context, channel string) ([]*Candidate, error) {
	lopt, err := u.listOptions(channel)
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Usage, err)
	}

	type outcome struct {
		name string
		rs   []Release
		err  error
		d    time.Duration
	}
	results := make([]outcome, len(u.Sources))
	var wg sync.WaitGroup
	for i, s := range u.Sources {
		wg.Add(1)
		go func(i int, s Source) {
			defer wg.Done()
			start := time.Now()
			rs, err := u.listSource(ctx, s, channel, lopt)
			results[i] = outcome{name: s.Name(), rs: rs, err: err, d: time.Since(start)}
		}(i, s)
	}
	wg.Wait()

	byVersion := map[int64]*Candidate{}
	var errs []error
	ok := 0
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.name, r.err))
			u.Log.Warn("source failed", "source", r.name, "err", r.err)
			continue
		}
		ok++
		u.mu.Lock()
		u.latency[r.name] = r.d
		u.mu.Unlock()
		for _, rel := range r.rs {
			key := rel.Version.Key()
			c := byVersion[key]
			if c == nil {
				c = &Candidate{Version: rel.Version}
				byVersion[key] = c
			}
			c.add(rel)
		}
	}
	if ok == 0 {
		return nil, exitcode.Wrap(exitcode.Unreachable, fmt.Errorf("every update source failed: %w", errors.Join(errs...)))
	}

	out := make([]*Candidate, 0, len(byVersion))
	for _, c := range byVersion {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version.After(out[j].Version) })
	return out, nil
}

func (u *Updater) listSource(ctx context.Context, s Source, channel string, lopt ListOptions) ([]Release, error) {
	cached, etag, fresh := u.Cache.LoadReleases(s.Name(), channel)
	if fresh && len(cached) > 0 {
		return cached, nil
	}
	lopt.ETag = etag
	res, err := s.List(ctx, lopt)
	if err != nil {
		if len(cached) > 0 {
			// Stale metadata beats no metadata: this is what makes the menu
			// usable on a plane.
			u.Log.Warn("using stale cached metadata", "source", s.Name(), "err", err)
			return cached, nil
		}
		return nil, err
	}
	if res.NotModified {
		u.Cache.TouchMeta(s.Name(), channel)
		return cached, nil
	}
	if err := u.Cache.SaveReleases(s.Name(), channel, res.Releases, res.ETag); err != nil {
		u.Log.Warn("could not cache release metadata", "source", s.Name(), "err", err)
	}
	return res.Releases, nil
}

// Plan is the decision an update run would make, without doing anything.
type Plan struct {
	Current   version.Version
	Target    *Candidate
	Channel   string
	UpToDate  bool
	Mandatory bool
	Reason    string
}

// Plan resolves what should happen. Every policy decision lives here so the
// CLI, the tray and the CI smoke test cannot drift apart.
func (u *Updater) Plan(ctx context.Context, opt Options) (*Plan, error) {
	channel := u.channel(opt)
	if err := u.LoadManifest(ctx); err != nil {
		return nil, err
	}
	cands, err := u.Available(ctx, channel)
	if err != nil {
		return nil, err
	}

	policy, hasPolicy := u.Manifest.Policy(channel)
	p := &Plan{Current: u.Current, Channel: channel}

	// Mandatory beats everything below: rollout, pause, snooze, user choice.
	if hasPolicy && !policy.MinSupported.IsZero() && u.Current.Before(policy.MinSupported) {
		p.Mandatory = true
		p.Reason = fmt.Sprintf("this version is below the minimum supported %s", policy.MinSupported)
	}

	// Drop anything the policy forbids.
	var usable []*Candidate
	for _, c := range cands {
		if u.Manifest.IsBlocked(c.Version) {
			continue
		}
		if hasPolicy {
			allowed := false
			for _, name := range c.SourceNames() {
				if policy.AllowsSource(name) {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		usable = append(usable, c)
	}

	// Explicit version: exact match or nothing. Never silently pick a
	// neighbour, the user asked for one specific build.
	if opt.TargetVersion != "" {
		want, perr := version.Parse(opt.TargetVersion)
		if perr != nil {
			return nil, exitcode.Wrap(exitcode.Usage, perr)
		}
		if u.Manifest.IsBlocked(want) {
			return nil, exitcode.Wrap(exitcode.NotFound, fmt.Errorf("version %s was withdrawn by policy", want))
		}
		for _, c := range usable {
			if c.Version.Equal(want) {
				if c.Version.Before(u.Current) && !opt.AllowDowngrade {
					return nil, exitcode.Wrap(exitcode.Usage, fmt.Errorf("%s is older than the installed %s: pass --allow-downgrade if that is intended", want, u.Current))
				}
				if !policy.MinSupported.IsZero() && c.Version.Before(policy.MinSupported) && !opt.Force {
					return nil, exitcode.Wrap(exitcode.Usage, fmt.Errorf("%s is below the minimum supported version %s", want, policy.MinSupported))
				}
				if c.Version.Equal(u.Current) {
					p.UpToDate = true
					p.Reason = "requested version is already installed"
					return p, nil
				}
				p.Target = c
				return p, nil
			}
		}
		return nil, exitcode.Wrap(exitcode.NotFound, fmt.Errorf("version %s is not available on any source (channel %s)", want, channel))
	}

	// Newest: take the highest version any source offers.
	var newest *Candidate
	for _, c := range usable {
		if c.Version.Before(u.Current) {
			continue
		}
		if newest == nil || c.Version.After(newest.Version) {
			newest = c
		}
	}
	if newest == nil || newest.Version.Equal(u.Current) {
		p.UpToDate = true
		if p.Reason == "" {
			p.Reason = "already on the newest available version"
		}
		return p, nil
	}

	// A policy "latest" lower than what the source has means the release is
	// being held back deliberately. Honour it, except on the internal channel
	// where the point is to test the unpublished build.
	if hasPolicy && !policy.Latest.IsZero() && newest.Version.After(policy.Latest) && channel != config.ChannelInternal {
		var capped *Candidate
		for _, c := range usable {
			if c.Version.Equal(policy.Latest) {
				capped = c
				break
			}
		}
		if capped == nil || !capped.Version.After(u.Current) {
			p.UpToDate = true
			p.Reason = fmt.Sprintf("policy holds this channel at %s", policy.Latest)
			return p, nil
		}
		newest = capped
	}

	forced := p.Mandatory || (hasPolicy && policy.ForceUpdate) || opt.Force

	if hasPolicy && policy.Paused && !forced {
		p.UpToDate = true
		p.Reason = "the channel is paused by policy"
		return p, nil
	}
	if hasPolicy && !InRollout(policy.Rollout(), newest.Version.String()) && !forced {
		p.UpToDate = true
		p.Reason = fmt.Sprintf("not in the %d%% rollout for %s yet", policy.Rollout(), newest.Version)
		return p, nil
	}
	if u.State != nil && u.State.FailedVersion.Equal(newest.Version) && !opt.Force {
		p.UpToDate = true
		p.Reason = fmt.Sprintf("%s already failed to install once, pass --force to try again", newest.Version)
		return p, nil
	}
	if !forced && u.Cfg.Snoozed() && opt.Silent {
		p.UpToDate = true
		p.Reason = fmt.Sprintf("snoozed until %s", u.Cfg.SnoozeUntil.Format(time.RFC1123))
		return p, nil
	}

	p.Target = newest
	if forced && p.Reason == "" {
		p.Reason = "required by policy"
	}
	return p, nil
}

// sourceOrder ranks sources fastest first, which is how a tie between two
// sources that both have the requested version gets broken.
func (u *Updater) sourceOrder(names []string) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := append([]string(nil), names...)
	sort.SliceStable(out, func(i, j int) bool {
		li, oki := u.latency[out[i]]
		lj, okj := u.latency[out[j]]
		switch {
		case oki && okj:
			return li < lj
		case oki:
			return true
		default:
			return false
		}
	})
	return out
}

func (u *Updater) sourceByName(name string) Source {
	for _, s := range u.Sources {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// pick chooses which source to download a candidate from, skipping any source
// that does not actually carry a build for this platform.
func (u *Updater) pick(c *Candidate) (Source, Release, Asset, error) {
	var last error
	for _, name := range u.sourceOrder(c.SourceNames()) {
		rel := c.BySource[name]
		src := u.sourceByName(name)
		if src == nil {
			continue
		}
		asset, err := rel.AssetFor(u.Cfg.AssetPrefix, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			last = err
			continue
		}
		return src, rel, asset, nil
	}
	if last == nil {
		last = fmt.Errorf("no source carries version %s", c.Version)
	}
	return nil, Release{}, Asset{}, exitcode.Wrap(exitcode.NotFound, last)
}

// digestFor returns the authoritative sha256 for an asset.
//
// The trust chain is always the same: a detached ed25519 signature over
// checksums.txt, and the digest of the binary read out of that file. A sha256
// embedded in a source index is treated as a hint only, because whoever can
// tamper with the index can tamper with the hint.
func (u *Updater) digestFor(ctx context.Context, src Source, rel Release, asset Asset) (string, error) {
	if !u.verifyEnabled() {
		u.Log.Warn("signature verification is disabled: this must never happen in a shipped build")
		if asset.SHA256 != "" {
			return asset.SHA256, nil
		}
		return "", exitcode.Wrap(exitcode.VerifyError, fmt.Errorf("no digest available for %s", asset.Name))
	}

	sumsAsset, ok := rel.AssetByName(ChecksumsFile)
	if !ok {
		return "", exitcode.Wrap(exitcode.VerifyError, fmt.Errorf("release %s has no %s", rel.Version, ChecksumsFile))
	}
	sigAsset, ok := rel.AssetByName(SignatureFile)
	if !ok {
		return "", exitcode.Wrap(exitcode.VerifyError, fmt.Errorf("release %s has no %s", rel.Version, SignatureFile))
	}

	sums, err := fetchToMemory(ctx, src, sumsAsset)
	if err != nil {
		return "", mapNetworkError(err)
	}
	rawSig, err := fetchToMemory(ctx, src, sigAsset)
	if err != nil {
		return "", mapNetworkError(err)
	}
	sig, err := DecodeSignature(rawSig)
	if err != nil {
		return "", exitcode.Wrap(exitcode.VerifyError, err)
	}
	if err := VerifySignature(sums, sig, u.Keys); err != nil {
		return "", exitcode.Wrap(exitcode.VerifyError, fmt.Errorf("%s of release %s: %w", ChecksumsFile, rel.Version, err))
	}
	table, err := ParseChecksums(sums)
	if err != nil {
		return "", exitcode.Wrap(exitcode.VerifyError, err)
	}
	digest, ok := table[asset.Name]
	if !ok {
		return "", exitcode.Wrap(exitcode.VerifyError, fmt.Errorf("%s has no entry for %s", ChecksumsFile, asset.Name))
	}
	return digest, nil
}

// Update runs the whole pipeline.
func (u *Updater) Update(ctx context.Context, opt Options) (*Result, error) {
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = u.Cfg.Timeout.Duration()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	lock, err := AcquireLock(filepath.Join(config.StateDir(), "update.lock"), 15*time.Minute)
	if err != nil {
		return nil, exitcode.Wrap(exitcode.Locked, err)
	}
	defer lock.Release()

	CleanupLeftovers()

	plan, err := u.Plan(ctx, opt)
	if err != nil {
		return nil, err
	}
	if u.State != nil {
		u.State.LastCheck = time.Now()
		_ = u.State.Save()
	}
	if plan.UpToDate {
		return &Result{From: u.Current, To: u.Current, UpToDate: true, Reason: plan.Reason}, nil
	}

	src, rel, asset, err := u.pick(plan.Target)
	if err != nil {
		return nil, err
	}
	res := &Result{From: u.Current, To: plan.Target.Version, Source: src.Name(), Asset: asset.Name, Reason: plan.Reason}

	digest, err := u.digestFor(ctx, src, rel, asset)
	if err != nil {
		return nil, err
	}

	res.FromCach = u.Cache.HasBlob(digest)
	if opt.DryRun {
		res.DryRun = true
		return res, nil
	}

	blob, err := u.downloadBlob(ctx, src, asset, digest, opt.Progress)
	if err != nil {
		if errors.Is(err, ErrChecksum) {
			return nil, exitcode.Wrap(exitcode.VerifyError, err)
		}
		return nil, mapNetworkError(err)
	}
	_ = u.Cache.SaveBlobInfo(BlobInfo{
		SHA256: digest, Version: plan.Target.Version, Asset: asset.Name, Source: src.Name(),
	})

	// Run the downloaded binary BEFORE touching the installed one. A build that
	// cannot even print its version must never reach the user's install path.
	if err := u.preflight(blob, plan.Target.Version); err != nil {
		u.markFailed(plan.Target.Version)
		return nil, exitcode.Wrap(exitcode.VerifyError, err)
	}

	applied, err := ApplyBinary(blob)
	if err != nil {
		return nil, exitcode.Wrap(exitcode.ApplyError, err)
	}
	res.Applied = true
	res.Backup = applied.Backup

	// Verify the installed binary as well: the copy could have been truncated,
	// or blocked by antivirus at exactly the wrong moment.
	if got, verr := VerifyBinary(applied.Exe); verr != nil || !strings.Contains(got, plan.Target.Version.String()) {
		if rerr := Rollback(applied); rerr != nil {
			return nil, exitcode.Wrap(exitcode.ApplyError,
				fmt.Errorf("the installed binary is broken (%v) and rollback failed (%v): restore %s manually", verr, rerr, applied.Backup))
		}
		u.markFailed(plan.Target.Version)
		return nil, exitcode.Wrap(exitcode.ApplyError,
			fmt.Errorf("the installed binary did not report %s (%v), rolled back to %s", plan.Target.Version, verr, u.Current))
	}

	if u.State != nil {
		u.State.LastUpdate = time.Now()
		u.State.LastVersion = plan.Target.Version
		u.State.FailedVersion = version.Version{}
		_ = u.State.Save()
	}
	if removed, perr := u.Cache.Prune(u.Cfg.KeepBlobs); perr == nil && removed > 0 {
		u.Log.Debug("pruned cached binaries", "count", removed)
	}
	return res, nil
}

// preflight executes the candidate binary in a temporary location.
func (u *Updater) preflight(blob string, want version.Version) error {
	dir, err := os.MkdirTemp("", "selfupdate-preflight-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	name := "candidate"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := copyFile(blob, path, 0o755); err != nil {
		return err
	}
	got, err := VerifyBinary(path)
	if err != nil {
		return err
	}
	if !strings.Contains(got, want.String()) {
		return fmt.Errorf("the downloaded binary reports %q but %s was expected", got, want)
	}
	return nil
}

func (u *Updater) markFailed(v version.Version) {
	if u.State == nil {
		return
	}
	u.State.FailedVersion = v
	_ = u.State.Save()
}

// Prefetch downloads newer versions in the background so a later update is
// instant. Only versions newer than the installed one are fetched: there is no
// reason to spend bandwidth on the past.
func (u *Updater) Prefetch(ctx context.Context, channel string, keep int) (int, error) {
	if keep <= 0 {
		keep = u.Cfg.PrefetchCount
	}
	if keep <= 0 {
		return 0, nil
	}
	if err := u.LoadManifest(ctx); err != nil {
		return 0, err
	}
	cands, err := u.Available(ctx, channel)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, c := range cands {
		if done >= keep {
			break
		}
		if !c.Version.After(u.Current) || u.Manifest.IsBlocked(c.Version) {
			continue
		}
		src, rel, asset, perr := u.pick(c)
		if perr != nil {
			continue
		}
		digest, derr := u.digestFor(ctx, src, rel, asset)
		if derr != nil {
			u.Log.Warn("prefetch skipped", "version", c.Version.String(), "err", derr)
			continue
		}
		if u.Cache.HasBlob(digest) {
			done++
			continue
		}
		if _, derr := u.downloadBlob(ctx, src, asset, digest, nil); derr != nil {
			u.Log.Warn("prefetch failed", "version", c.Version.String(), "err", derr)
			continue
		}
		_ = u.Cache.SaveBlobInfo(BlobInfo{SHA256: digest, Version: c.Version, Asset: asset.Name, Source: src.Name()})
		done++
	}
	_, _ = u.Cache.Prune(u.Cfg.KeepBlobs)
	return done, nil
}

// RollbackToBackup restores the previous binary kept next to the executable.
func (u *Updater) RollbackToBackup() (string, error) {
	backup, ok := BackupPath()
	if !ok {
		return "", exitcode.Wrap(exitcode.NotFound, fmt.Errorf("no previous binary is available to roll back to"))
	}
	exe, err := SelfExe()
	if err != nil {
		return "", err
	}
	if err := Rollback(Applied{Exe: exe, Backup: backup}); err != nil {
		return "", exitcode.Wrap(exitcode.ApplyError, err)
	}
	got, err := VerifyBinary(exe)
	if err != nil {
		return "", exitcode.Wrap(exitcode.ApplyError, err)
	}
	return got, nil
}

func mapNetworkError(err error) error {
	if errors.Is(err, ErrUnreachable) || errors.Is(err, context.DeadlineExceeded) {
		return exitcode.Wrap(exitcode.Unreachable, err)
	}
	if errors.Is(err, ErrSignature) || errors.Is(err, ErrChecksum) {
		return exitcode.Wrap(exitcode.VerifyError, err)
	}
	return err
}
