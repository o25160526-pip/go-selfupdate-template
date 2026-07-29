package updater

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// AzureSource reads releases from Azure DevOps.
//
// Azure has no equivalent of a GitHub "release", so there are two modes:
//
//	blob (default) - a signed index.json next to the binaries in Blob Storage or
//	                 any static host. Plain HTTP, works everywhere, trivial to
//	                 mirror, no preview APIs involved.
//	feed           - Universal Packages. Real package versioning, but listing
//	                 uses a preview API and downloads go through the upack
//	                 content endpoint. Experimental until the Azure side of the
//	                 pipeline is decided.
type AzureSource struct {
	Mode         string
	IndexURL     string
	Organization string
	Project      string
	Feed         string
	Package      string
	Token        string
	HTTP         *http.Client
}

func (a *AzureSource) Name() string { return "azure" }

func (a *AzureSource) headers() http.Header {
	h := http.Header{}
	h.Set("User-Agent", "go-selfupdate-template")
	if a.Token != "" {
		// Azure DevOps PATs are sent as basic auth with an empty username.
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+a.Token)))
	}
	return h
}

func (a *AzureSource) List(ctx context.Context, opt ListOptions) (ListResult, error) {
	switch a.Mode {
	case config.AzureModeFeed:
		return a.listFeed(ctx, opt)
	default:
		return a.listBlob(ctx, opt)
	}
}

// ---- blob mode ----

// AzureIndex is the shape of index.json.
type AzureIndex struct {
	Schema   int               `json:"schema"`
	Releases []AzureIndexEntry `json:"releases"`
}

type AzureIndexEntry struct {
	Version     version.Version   `json:"version"`
	PublishedAt time.Time         `json:"published_at,omitempty"`
	Prerelease  bool              `json:"prerelease,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	Assets      []AzureIndexAsset `json:"assets"`
}

type AzureIndexAsset struct {
	Name   string `json:"name"`
	URL    string `json:"url"` // absolute, or relative to index.json
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

func (a *AzureSource) listBlob(ctx context.Context, opt ListOptions) (ListResult, error) {
	if a.IndexURL == "" {
		return ListResult{}, fmt.Errorf("azure: index_url is not configured")
	}
	h := a.headers()
	h.Set("Accept", "application/json")
	if opt.ETag != "" {
		h.Set("If-None-Match", opt.ETag)
	}
	res, err := doGet(ctx, a.HTTP, a.IndexURL, h)
	if err != nil {
		return ListResult{}, fmt.Errorf("azure: %w", err)
	}
	if res.NotModified {
		return ListResult{NotModified: true, ETag: opt.ETag}, nil
	}

	var idx AzureIndex
	if err := json.Unmarshal(res.Body, &idx); err != nil {
		return ListResult{}, fmt.Errorf("azure: parse index.json: %w", err)
	}
	if idx.Schema != 1 {
		return ListResult{}, fmt.Errorf("azure: index.json schema %d is not supported", idx.Schema)
	}

	base, err := url.Parse(a.IndexURL)
	if err != nil {
		return ListResult{}, err
	}

	out := ListResult{ETag: res.ETag}
	for _, e := range idx.Releases {
		if e.Prerelease && !opt.IncludePrerelease {
			continue
		}
		rel := Release{
			Version:     e.Version,
			Source:      a.Name(),
			Tag:         e.Version.Tag(),
			Prerelease:  e.Prerelease,
			Notes:       e.Notes,
			PublishedAt: e.PublishedAt,
		}
		for _, as := range e.Assets {
			abs := as.URL
			if u, uerr := url.Parse(as.URL); uerr == nil {
				abs = base.ResolveReference(u).String()
			}
			goos, goarch, _ := classifyAsset(as.Name)
			rel.Assets = append(rel.Assets, Asset{
				Name:   as.Name,
				URL:    abs,
				Size:   as.Size,
				SHA256: strings.ToLower(as.SHA256),
				OS:     goos,
				Arch:   goarch,
			})
		}
		out.Releases = append(out.Releases, rel)
	}
	return out, nil
}

// ---- feed mode (Universal Packages, experimental) ----

func (a *AzureSource) listFeed(ctx context.Context, opt ListOptions) (ListResult, error) {
	if a.Organization == "" || a.Feed == "" || a.Package == "" {
		return ListResult{}, fmt.Errorf("azure feed mode needs organization, feed and package")
	}
	project := ""
	if a.Project != "" {
		project = url.PathEscape(a.Project) + "/"
	}
	u := fmt.Sprintf("https://feeds.dev.azure.com/%s/%s_apis/packaging/Feeds/%s/packages?packageNameQuery=%s&includeAllVersions=true&api-version=7.1-preview.1",
		url.PathEscape(a.Organization), project, url.PathEscape(a.Feed), url.QueryEscape(a.Package))

	h := a.headers()
	h.Set("Accept", "application/json")
	body, err := getBytes(ctx, a.HTTP, u, h)
	if err != nil {
		return ListResult{}, fmt.Errorf("azure feed: %w", err)
	}

	var payload struct {
		Value []struct {
			Name     string `json:"name"`
			Versions []struct {
				Version     string    `json:"version"`
				PublishDate time.Time `json:"publishDate"`
				IsListed    bool      `json:"isListed"`
				IsDeleted   bool      `json:"isDeleted"`
			} `json:"versions"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ListResult{}, fmt.Errorf("azure feed: parse packages: %w", err)
	}

	var out ListResult
	for _, pkg := range payload.Value {
		if !strings.EqualFold(pkg.Name, a.Package) {
			continue
		}
		for _, pv := range pkg.Versions {
			if pv.IsDeleted || !pv.IsListed {
				continue
			}
			v, verr := version.Parse(pv.Version)
			if verr != nil {
				continue
			}
			rel := Release{
				Version:     v,
				Source:      a.Name(),
				Tag:         v.Tag(),
				PublishedAt: pv.PublishDate,
			}
			// Universal Packages has no per-file listing endpoint, so asset
			// names come from the naming convention this template publishes.
			names := append(platformAssetNames(a.Package), ChecksumsFile, SignatureFile)
			for _, name := range names {
				goos, goarch, _ := classifyAsset(name)
				rel.Assets = append(rel.Assets, Asset{
					Name: name,
					URL:  a.upackContentURL(pv.Version, name),
					OS:   goos,
					Arch: goarch,
				})
			}
			out.Releases = append(out.Releases, rel)
		}
	}
	return out, nil
}

func (a *AzureSource) upackContentURL(ver, file string) string {
	project := ""
	if a.Project != "" {
		project = url.PathEscape(a.Project) + "/"
	}
	return fmt.Sprintf("https://pkgs.dev.azure.com/%s/%s_apis/packaging/feeds/%s/upack/packages/%s/versions/%s/content?path=%s&api-version=7.1-preview.2",
		url.PathEscape(a.Organization), project, url.PathEscape(a.Feed),
		url.PathEscape(strings.ToLower(a.Package)), url.PathEscape(ver), url.QueryEscape("/"+file))
}

// platformAssetNames lists the six targets this template builds.
func platformAssetNames(prefix string) []string {
	targets := [][2]string{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
	}
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, BinaryName(prefix, t[0], t[1]))
	}
	return out
}

func (a *AzureSource) Fetch(ctx context.Context, as Asset, w io.Writer, offset int64) error {
	u := as.downloadURL(false)
	if u == "" {
		return fmt.Errorf("azure: asset %q has no download url", as.Name)
	}
	h := a.headers()
	h.Set("Accept", "application/octet-stream")
	if offset > 0 {
		h.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	return streamTo(ctx, a.HTTP, u, h, w, offset)
}
