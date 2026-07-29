package updater

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// Names of the metadata assets that form the trust chain. Every source must
// publish both alongside the binaries.
const (
	ChecksumsFile  = "checksums.txt"
	SignatureFile  = "checksums.txt.sig"
	IndexFileName  = "index.json"
	assetSeparator = "_"
)

// Release is one published version as seen from one source.
type Release struct {
	Version     version.Version `json:"version"`
	Source      string          `json:"source"`
	Tag         string          `json:"tag,omitempty"`
	Draft       bool            `json:"draft,omitempty"`
	Prerelease  bool            `json:"prerelease,omitempty"`
	Notes       string          `json:"notes,omitempty"`
	PublishedAt time.Time       `json:"published_at,omitempty"`
	Assets      []Asset         `json:"assets,omitempty"`
}

// Asset is a downloadable file inside a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// APIURL is used when the public download URL does not work, which is the
	// case for assets of a GitHub draft release.
	APIURL string `json:"api_url,omitempty"`
	Size   int64  `json:"size,omitempty"`
	// SHA256 coming from a source index is only a hint. The authoritative
	// digest always comes from the signed checksums.txt.
	SHA256 string `json:"sha256,omitempty"`
	OS     string `json:"os,omitempty"`
	Arch   string `json:"arch,omitempty"`
}

func (a Asset) downloadURL(authenticated bool) string {
	if authenticated && a.APIURL != "" {
		return a.APIURL
	}
	if a.URL != "" {
		return a.URL
	}
	return a.APIURL
}

// AssetByName finds a metadata asset such as checksums.txt.
func (r Release) AssetByName(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return Asset{}, false
}

// BinaryName is the asset name expected for a platform.
func BinaryName(prefix, goos, goarch string) string {
	name := prefix + assetSeparator + goos + assetSeparator + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// SelfBinaryName is BinaryName for the running platform.
func SelfBinaryName(prefix string) string { return BinaryName(prefix, runtime.GOOS, runtime.GOARCH) }

// AssetFor picks the binary matching a platform.
//
// If nothing matches we return an error instead of grabbing whatever is there.
// Installing an amd64 build onto arm64 produces a binary that cannot exec, and
// by that point the user's working install is already gone.
func (r Release) AssetFor(prefix, goos, goarch string) (Asset, error) {
	want := BinaryName(prefix, goos, goarch)
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, want) {
			a.OS, a.Arch = goos, goarch
			return a, nil
		}
	}
	// Fall back to parsing names, in case the prefix was renamed downstream.
	for _, a := range r.Assets {
		if aos, aarch, ok := classifyAsset(a.Name); ok && aos == goos && aarch == goarch {
			a.OS, a.Arch = aos, aarch
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s has no build for %s/%s (available: %s)",
		r.Version, goos, goarch, strings.Join(r.binaryNames(), ", "))
}

func (r Release) binaryNames() []string {
	var out []string
	for _, a := range r.Assets {
		if _, _, ok := classifyAsset(a.Name); ok {
			out = append(out, a.Name)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

var knownOS = map[string]bool{"linux": true, "darwin": true, "windows": true, "freebsd": true}

var knownArch = map[string]bool{"amd64": true, "arm64": true, "386": true, "arm": true}

// classifyAsset parses <anything>_<os>_<arch>[.exe].
func classifyAsset(name string) (goos, goarch string, ok bool) {
	n := strings.TrimSuffix(name, ".exe")
	parts := strings.Split(n, assetSeparator)
	if len(parts) < 3 {
		return "", "", false
	}
	goos, goarch = parts[len(parts)-2], parts[len(parts)-1]
	if !knownOS[goos] || !knownArch[goarch] {
		return "", "", false
	}
	// A windows asset must end in .exe and a non-windows asset must not.
	if (goos == "windows") != strings.HasSuffix(name, ".exe") {
		return "", "", false
	}
	return goos, goarch, true
}

// Candidate is one version merged across every source that offers it.
type Candidate struct {
	Version     version.Version
	BySource    map[string]Release
	Notes       string
	PublishedAt time.Time
	Draft       bool
	Prerelease  bool
}

func (c *Candidate) add(r Release) {
	if c.BySource == nil {
		c.BySource = map[string]Release{}
	}
	c.BySource[r.Source] = r
	if c.Notes == "" {
		c.Notes = r.Notes
	}
	if c.PublishedAt.IsZero() || (!r.PublishedAt.IsZero() && r.PublishedAt.Before(c.PublishedAt)) {
		c.PublishedAt = r.PublishedAt
	}
	c.Draft = c.Draft || r.Draft
	c.Prerelease = c.Prerelease || r.Prerelease
}

// SourceNames returns the sources offering this version, sorted for stable output.
func (c *Candidate) SourceNames() []string {
	out := make([]string, 0, len(c.BySource))
	for name := range c.BySource {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
