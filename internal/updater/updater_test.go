package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/exitcode"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// fakeSource is an in-memory Source. Tests use it instead of hitting the
// network so failover behaviour can be asserted deterministically.
type fakeSource struct {
	name     string
	versions []string
	files    map[string][]byte
	fail     error
	delay    time.Duration
	calls    int
}

func newFakeSource(name string, priv ed25519.PrivateKey, versions ...string) *fakeSource {
	f := &fakeSource{name: name, versions: versions, files: map[string][]byte{}}
	for _, v := range versions {
		bin := []byte("binary-of-" + v)
		binName := BinaryName("app", "linux", "amd64")
		f.files[v+"/"+binName] = bin
		sum := sha256.Sum256(bin)
		sums := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), binName))
		f.files[v+"/"+ChecksumsFile] = sums
		f.files[v+"/"+SignatureFile] = ed25519.Sign(priv, sums)
	}
	return f
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) List(ctx context.Context, opt ListOptions) (ListResult, error) {
	f.calls++
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.fail != nil {
		return ListResult{}, f.fail
	}
	var out ListResult
	for _, v := range f.versions {
		pv := version.MustParse(v)
		rel := Release{Version: pv, Source: f.name, Tag: pv.Tag()}
		for _, name := range []string{BinaryName("app", "linux", "amd64"), ChecksumsFile, SignatureFile} {
			rel.Assets = append(rel.Assets, Asset{Name: name, URL: v + "/" + name})
		}
		out.Releases = append(out.Releases, rel)
	}
	return out, nil
}

func (f *fakeSource) Fetch(ctx context.Context, a Asset, w io.Writer, offset int64) error {
	if f.fail != nil {
		return f.fail
	}
	b, ok := f.files[a.URL]
	if !ok {
		return fmt.Errorf("fake: %s not found", a.URL)
	}
	if offset > int64(len(b)) {
		return errRestartDownload
	}
	_, err := w.Write(b[offset:])
	return err
}

func testUpdater(t *testing.T, current string, keys []ed25519.PublicKey, sources ...Source) *Updater {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.AssetPrefix = "app"
	cfg.Channel = config.ChannelStable
	cfg.RequireSignature = true
	return &Updater{
		Cfg:     cfg,
		Sources: sources,
		Cache:   NewCache(dir, time.Millisecond, 3),
		Keys:    keys,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Current: version.MustParse(current),
		State:   LoadState(dir),
		latency: map[string]time.Duration{},
	}
}

func TestPlanPicksNewestAcrossSources(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0701.0000", "1.26.0710.0000")
	az := newFakeSource("azure", priv, "1.26.0729.1930")
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, gh, az)

	plan, err := u.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.UpToDate || plan.Target.Version.String() != "1.26.0729.1930" {
		t.Fatalf("expected the azure version to win, got %+v", plan)
	}
}

func TestPlanSurvivesOneDeadSource(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	dead := newFakeSource("azure", priv, "1.26.0729.1930")
	dead.fail = fmt.Errorf("%w: connection refused", ErrUnreachable)
	alive := newFakeSource("github", priv, "1.26.0710.0000")
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, dead, alive)

	plan, err := u.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatalf("one dead source must not fail the run: %v", err)
	}
	if plan.Target == nil || plan.Target.Version.String() != "1.26.0710.0000" {
		t.Fatalf("expected fallback to github, got %+v", plan)
	}
}

func TestPlanAllSourcesDeadIsUnreachable(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	a := newFakeSource("github", priv, "1.26.0710.0000")
	a.fail = fmt.Errorf("%w: dns", ErrUnreachable)
	b := newFakeSource("azure", priv, "1.26.0729.1930")
	b.fail = fmt.Errorf("%w: dns", ErrUnreachable)
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, a, b)

	_, err := u.Plan(context.Background(), Options{})
	if code := exitcode.From(err); code != exitcode.Unreachable {
		t.Fatalf("expected exit code %d, got %d (%v)", exitcode.Unreachable, code, err)
	}
}

func TestPlanExactVersionFromWhicheverSourceHasIt(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0710.0000")
	az := newFakeSource("azure", priv, "1.26.0729.1930")
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, gh, az)

	plan, err := u.Plan(context.Background(), Options{TargetVersion: "1.26.0729.1930"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target.SourceNames()[0] != "azure" {
		t.Fatalf("expected azure to serve it, got %v", plan.Target.SourceNames())
	}

	_, err = u.Plan(context.Background(), Options{TargetVersion: "1.26.0101.0000"})
	if code := exitcode.From(err); code != exitcode.NotFound {
		t.Fatalf("expected exit code %d for a missing version, got %d (%v)", exitcode.NotFound, code, err)
	}
}

func TestPlanRefusesSilentDowngrade(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0701.0000", "1.26.0729.1930")
	u := testUpdater(t, "1.26.0729.1930", []ed25519.PublicKey{pub}, gh)

	if _, err := u.Plan(context.Background(), Options{TargetVersion: "1.26.0701.0000"}); err == nil {
		t.Fatal("a downgrade without --allow-downgrade must be refused")
	}
	plan, err := u.Plan(context.Background(), Options{TargetVersion: "1.26.0701.0000", AllowDowngrade: true})
	if err != nil {
		t.Fatalf("explicit downgrade should be allowed: %v", err)
	}
	if plan.Target.Version.String() != "1.26.0701.0000" {
		t.Fatalf("got %s", plan.Target.Version)
	}
}

func TestPlanHonoursBlockedAndMinSupported(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0710.0000", "1.26.0729.1930")
	u := testUpdater(t, "1.26.0101.0000", []ed25519.PublicKey{pub}, gh)
	rollout := 0 // rollout halted, but mandatory must still win
	u.Manifest = &Manifest{
		Schema:   1,
		IssuedAt: time.Now(),
		Channels: map[string]ChannelPolicy{"stable": {
			MinSupported:   version.MustParse("1.26.0601.0000"),
			RolloutPercent: &rollout,
		}},
		Blocked: []version.Version{version.MustParse("1.26.0729.1930")},
	}

	plan, err := u.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Mandatory {
		t.Fatal("being below min_supported must be mandatory")
	}
	if plan.Target == nil || plan.Target.Version.String() != "1.26.0710.0000" {
		t.Fatalf("the blocked version must be skipped, got %+v", plan.Target)
	}
}

func TestPlanRolloutHoldsBack(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0729.1930")
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, gh)
	rollout := 0
	u.Manifest = &Manifest{
		Schema:   1,
		IssuedAt: time.Now(),
		Channels: map[string]ChannelPolicy{"stable": {RolloutPercent: &rollout}},
	}
	plan, err := u.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.UpToDate {
		t.Fatal("a 0% rollout must hold the client back")
	}
	// force_update overrides the rollout.
	u.Manifest.Channels["stable"] = ChannelPolicy{RolloutPercent: &rollout, ForceUpdate: true}
	plan, err = u.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.UpToDate {
		t.Fatal("force_update must beat the rollout percentage")
	}
}

func TestPlanPolicyCapsLatest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0710.0000", "1.26.0729.1930")
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, gh)
	u.Manifest = &Manifest{
		Schema:   1,
		IssuedAt: time.Now(),
		Channels: map[string]ChannelPolicy{"stable": {Latest: version.MustParse("1.26.0710.0000")}},
	}
	plan, err := u.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target == nil || plan.Target.Version.String() != "1.26.0710.0000" {
		t.Fatalf("policy must hold the channel back, got %+v", plan.Target)
	}
}

func TestDigestRejectsTamperedChecksums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0729.1930")
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, gh)

	// Tamper with checksums.txt but keep the old signature.
	gh.files["1.26.0729.1930/"+ChecksumsFile] = []byte(
		"0000000000000000000000000000000000000000000000000000000000000000  app_linux_amd64\n")

	plan, err := u.Plan(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	src, rel, asset, err := u.pick(plan.Target)
	if err != nil {
		// pick fails on non-linux/amd64 hosts, which is fine: nothing to assert.
		t.Skipf("host platform has no fake asset: %v", err)
	}
	_, err = u.digestFor(context.Background(), src, rel, asset)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("expected a signature failure, got %v", err)
	}
	if code := exitcode.From(err); code != exitcode.VerifyError {
		t.Fatalf("expected exit code %d, got %d", exitcode.VerifyError, code)
	}
}

func TestDownloadResumesAndVerifies(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0729.1930")
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, gh)

	binName := BinaryName("app", "linux", "amd64")
	body := gh.files["1.26.0729.1930/"+binName]
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	asset := Asset{Name: binName, URL: "1.26.0729.1930/" + binName, Size: int64(len(body))}

	// Seed a partial download to exercise the resume path.
	if err := os.MkdirAll(u.Cache.blobsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.Cache.BlobPath(digest)+".part", body[:4], 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := u.downloadBlob(context.Background(), gh, asset, digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := SHA256File(path)
	if err != nil || got != digest {
		t.Fatalf("resumed download is corrupt: %v %s", err, got)
	}

	// A wrong digest must fail and must not leave the bad bytes behind.
	bad := "1111111111111111111111111111111111111111111111111111111111111111"
	if _, err := u.downloadBlob(context.Background(), gh, asset, bad, nil); !errors.Is(err, ErrChecksum) {
		t.Fatalf("expected a checksum failure, got %v", err)
	}
	if _, err := os.Stat(u.Cache.BlobPath(bad) + ".part"); !os.IsNotExist(err) {
		t.Fatal("corrupt .part file was left on disk")
	}
}

func TestCacheMakesTheSecondCheckFree(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0729.1930")
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, gh)
	u.Cache.TTL = time.Hour

	if _, err := u.Available(context.Background(), config.ChannelStable); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Available(context.Background(), config.ChannelStable); err != nil {
		t.Fatal(err)
	}
	if gh.calls != 1 {
		t.Fatalf("expected 1 network call thanks to the cache, got %d", gh.calls)
	}
}

func TestInternalChannelNeedsAToken(t *testing.T) {
	t.Setenv("APP_UPDATE_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	pub, priv, _ := ed25519.GenerateKey(nil)
	gh := newFakeSource("github", priv, "1.26.0729.1930")
	u := testUpdater(t, "1.26.0701.0000", []ed25519.PublicKey{pub}, gh)

	if _, err := u.Available(context.Background(), config.ChannelInternal); err == nil {
		t.Fatal("the internal channel must refuse to run without a token")
	}
}

func TestRolloutBucketsAreStable(t *testing.T) {
	first := RolloutBucket("machine-42", "1.26.0729.1930")
	for i := 0; i < 100; i++ {
		if RolloutBucket("machine-42", "1.26.0729.1930") != first {
			t.Fatal("bucket assignment must be stable across calls")
		}
	}
	// A different target reshuffles, so the same clients are not always last.
	differ := 0
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("machine-%d", i)
		if RolloutBucket(id, "a") != RolloutBucket(id, "b") {
			differ++
		}
	}
	if differ < 25 {
		t.Fatalf("expected the salt to reshuffle buckets, only %d/50 changed", differ)
	}
}

func TestAssetForRefusesWrongArch(t *testing.T) {
	rel := Release{
		Version: version.MustParse("1.26.0729.1930"),
		Assets: []Asset{
			{Name: "app_linux_amd64"},
			{Name: "app_windows_arm64.exe"},
		},
	}
	if _, err := rel.AssetFor("app", "darwin", "arm64"); err == nil {
		t.Fatal("must refuse rather than install a binary for another platform")
	}
	if a, err := rel.AssetFor("app", "windows", "arm64"); err != nil || a.Name != "app_windows_arm64.exe" {
		t.Fatalf("windows/arm64 lookup failed: %v %+v", err, a)
	}
}

func TestClassifyAsset(t *testing.T) {
	cases := map[string][2]string{
		"app_linux_amd64":         {"linux", "amd64"},
		"app_darwin_arm64":        {"darwin", "arm64"},
		"app_windows_amd64.exe":   {"windows", "amd64"},
		"my-tool_linux_arm64":     {"linux", "arm64"},
	}
	for name, want := range cases {
		goos, goarch, ok := classifyAsset(name)
		if !ok || goos != want[0] || goarch != want[1] {
			t.Errorf("%s -> %s/%s ok=%v", name, goos, goarch, ok)
		}
	}
	bad := []string{"checksums.txt", "app_linux_amd64.exe", "app_windows_amd64", "app", "app_plan9_amd64"}
	for _, name := range bad {
		if _, _, ok := classifyAsset(name); ok {
			t.Errorf("%s should not classify as a platform binary", name)
		}
	}
}

func TestLockIsExclusive(t *testing.T) {
	path := t.TempDir() + "/update.lock"
	l, err := AcquireLock(path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(path, time.Minute); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if l2, err := AcquireLock(path, time.Minute); err != nil {
		t.Fatalf("lock should be free again: %v", err)
	} else {
		l2.Release()
	}
}
