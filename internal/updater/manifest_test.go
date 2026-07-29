package updater

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

func validManifest(t *testing.T) []byte {
	t.Helper()
	rollout := 50
	m := Manifest{
		Schema:   1,
		IssuedAt: time.Now().Add(-time.Hour).UTC().Truncate(time.Second),
		Channels: map[string]ChannelPolicy{
			"stable": {
				Latest:         version.MustParse("1.26.0729.1930"),
				MinSupported:   version.MustParse("1.26.0701.0000"),
				ForceUpdate:    true,
				RolloutPercent: &rollout,
				Sources:        []string{"github", "azure"},
			},
		},
		Blocked: []version.Version{version.MustParse("1.26.0715.1200")},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest(validManifest(t), DefaultManifestMaxAge)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := m.Policy("stable")
	if !ok || p.Latest.String() != "1.26.0729.1930" || p.Rollout() != 50 {
		t.Fatalf("bad policy: %+v", p)
	}
	if !m.IsBlocked(version.MustParse("1.26.0715.1200")) {
		t.Fatal("blocked version not detected")
	}
	if m.IsBlocked(version.MustParse("1.26.0729.1930")) {
		t.Fatal("false positive on blocked list")
	}
}

func TestRolloutZeroIsNotUnset(t *testing.T) {
	zero := 0
	if got := (ChannelPolicy{RolloutPercent: &zero}).Rollout(); got != 0 {
		t.Fatalf("explicit 0 must halt the rollout, got %d", got)
	}
	if got := (ChannelPolicy{}).Rollout(); got != 100 {
		t.Fatalf("unset must mean 100, got %d", got)
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := map[string]string{
		"wrong schema":  `{"schema":2,"issued_at":"2026-07-29T00:00:00Z","channels":{"stable":{"latest":"1.26.0729.1930"}}}`,
		"no channels":   `{"schema":1,"issued_at":"2026-07-29T00:00:00Z","channels":{}}`,
		"no issued_at":  `{"schema":1,"channels":{"stable":{"latest":"1.26.0729.1930"}}}`,
		"unknown field": `{"schema":1,"issued_at":"2026-07-29T00:00:00Z","channels":{"stable":{"latest":"1.26.0729.1930"}},"typo":1}`,
		"bad version":   `{"schema":1,"issued_at":"2026-07-29T00:00:00Z","channels":{"stable":{"latest":"nightly"}}}`,
	}
	for name, body := range cases {
		if _, err := ParseManifest([]byte(body), 0); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseManifestRejectsStale(t *testing.T) {
	body := `{"schema":1,"issued_at":"2020-01-01T00:00:00Z","channels":{"stable":{"latest":"1.26.0729.1930"}}}`
	if _, err := ParseManifest([]byte(body), 24*time.Hour); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected staleness error, got %v", err)
	}
}

func TestFetchManifestSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	body := validManifest(t)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sig") {
			w.Write([]byte(sig))
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	f := &ManifestFetcher{HTTP: srv.Client(), Keys: []ed25519.PublicKey{pub}}
	if _, err := f.Fetch(context.Background(), srv.URL+"/manifest.json"); err != nil {
		t.Fatalf("good signature rejected: %v", err)
	}

	otherPub, _, _ := ed25519.GenerateKey(nil)
	f.Keys = []ed25519.PublicKey{otherPub}
	if _, err := f.Fetch(context.Background(), srv.URL+"/manifest.json"); err == nil {
		t.Fatal("wrong key accepted")
	}

	// Rotation: a client that trusts [old, new] accepts something signed by new.
	f.Keys = []ed25519.PublicKey{otherPub, pub}
	if _, err := f.Fetch(context.Background(), srv.URL+"/manifest.json"); err != nil {
		t.Fatalf("rotation key rejected: %v", err)
	}
}

func TestVerifyWithoutKeysFails(t *testing.T) {
	if err := VerifySignature([]byte("x"), make([]byte, ed25519.SignatureSize), nil); err != ErrNoTrustedKeys {
		t.Fatalf("expected ErrNoTrustedKeys, got %v", err)
	}
}

func TestParseChecksums(t *testing.T) {
	in := "# comment\n" +
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  dist/app_linux_amd64\n" +
		"E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855 *app_windows_amd64.exe\n" +
		"garbage line\n"
	sums, err := ParseChecksums([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("got %d entries: %v", len(sums), sums)
	}
	if sums["app_linux_amd64"] == "" || sums["app_windows_amd64.exe"] != strings.ToLower(sums["app_linux_amd64"]) {
		t.Fatalf("bad parse: %v", sums)
	}
}
