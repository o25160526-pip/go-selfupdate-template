package updater

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// ManifestSchema is the only schema version this client understands.
const ManifestSchema = 1

// DefaultManifestMaxAge rejects a policy that is suspiciously old. Without
// this, an attacker who can serve a stale file forever can also suppress a
// kill switch forever.
const DefaultManifestMaxAge = 30 * 24 * time.Hour

// Manifest is the update policy, hosted separately from the releases (gh-pages
// or a blob container) so policy can change without shipping a new build.
type Manifest struct {
	Schema   int                      `json:"schema"`
	IssuedAt time.Time                `json:"issued_at"`
	Channels map[string]ChannelPolicy `json:"channels"`
	Blocked  []version.Version        `json:"blocked,omitempty"`
	Message  string                   `json:"message,omitempty"`
}

type ChannelPolicy struct {
	Latest       version.Version `json:"latest"`
	MinSupported version.Version `json:"min_supported,omitempty"`
	ForceUpdate  bool            `json:"force_update,omitempty"`
	// RolloutPercent is a pointer so "0" (halt the rollout) is distinguishable
	// from "not set" (treated as 100).
	RolloutPercent *int     `json:"rollout_percent,omitempty"`
	Sources        []string `json:"sources,omitempty"`
	Paused         bool     `json:"paused,omitempty"`
	Notes          string   `json:"notes,omitempty"`
}

func (p ChannelPolicy) Rollout() int {
	if p.RolloutPercent == nil {
		return 100
	}
	if *p.RolloutPercent < 0 {
		return 0
	}
	if *p.RolloutPercent > 100 {
		return 100
	}
	return *p.RolloutPercent
}

// IsBlocked reports whether a version was pulled with the kill switch.
func (m *Manifest) IsBlocked(v version.Version) bool {
	if m == nil {
		return false
	}
	for _, b := range m.Blocked {
		if b.Equal(v) {
			return true
		}
	}
	return false
}

// Policy returns the policy for a channel, falling back to stable.
func (m *Manifest) Policy(channel string) (ChannelPolicy, bool) {
	if m == nil {
		return ChannelPolicy{}, false
	}
	if p, ok := m.Channels[channel]; ok {
		return p, true
	}
	p, ok := m.Channels["stable"]
	return p, ok
}

// AllowsSource reports whether a channel may use a given source name.
// An empty list means "any source".
func (p ChannelPolicy) AllowsSource(name string) bool {
	if len(p.Sources) == 0 {
		return true
	}
	for _, s := range p.Sources {
		if s == name {
			return true
		}
	}
	return false
}

type ManifestFetcher struct {
	HTTP   *http.Client
	Keys   []ed25519.PublicKey
	MaxAge time.Duration
	// SkipVerify is for local development only.
	SkipVerify bool
}

// Fetch downloads manifest.json plus the detached manifest.json.sig and
// verifies the signature before parsing.
//
// Detached signatures are used deliberately: an embedded "signature" field
// would require canonical JSON serialisation to verify, and every homegrown
// canonicaliser eventually disagrees with itself across languages.
func (f *ManifestFetcher) Fetch(ctx context.Context, url string) (*Manifest, error) {
	if url == "" {
		return nil, fmt.Errorf("no manifest url configured")
	}
	client := f.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	body, err := getBytes(ctx, client, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}

	if !f.SkipVerify {
		rawSig, err := getBytes(ctx, client, url+".sig", nil)
		if err != nil {
			return nil, fmt.Errorf("fetch manifest signature: %w", err)
		}
		sig, err := DecodeSignature(rawSig)
		if err != nil {
			return nil, err
		}
		if err := VerifySignature(body, sig, f.Keys); err != nil {
			return nil, fmt.Errorf("manifest: %w", err)
		}
	}

	return ParseManifest(body, f.maxAge())
}

func (f *ManifestFetcher) maxAge() time.Duration {
	if f.MaxAge > 0 {
		return f.MaxAge
	}
	return DefaultManifestMaxAge
}

// ParseManifest validates structure and freshness.
func ParseManifest(body []byte, maxAge time.Duration) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytesReader(body))
	dec.DisallowUnknownFields() // catch typos in policy files before they ship
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Schema != ManifestSchema {
		return nil, fmt.Errorf("manifest schema %d is not supported by this client (want %d)", m.Schema, ManifestSchema)
	}
	if len(m.Channels) == 0 {
		return nil, fmt.Errorf("manifest defines no channels")
	}
	if m.IssuedAt.IsZero() {
		return nil, fmt.Errorf("manifest is missing issued_at")
	}
	now := time.Now()
	if maxAge > 0 && now.Sub(m.IssuedAt) > maxAge {
		return nil, fmt.Errorf("manifest is stale: issued %s ago (max %s)", now.Sub(m.IssuedAt).Round(time.Hour), maxAge)
	}
	if m.IssuedAt.After(now.Add(24 * time.Hour)) {
		return nil, fmt.Errorf("manifest issued_at is in the future (%s): check the signing host clock", m.IssuedAt)
	}
	return &m, nil
}
