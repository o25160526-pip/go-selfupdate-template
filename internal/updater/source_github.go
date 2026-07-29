package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// GitHubSource reads GitHub Releases.
//
// Draft releases are only visible to a token with repo scope. That is exactly
// why a shipped binary must never contain one: the `internal` channel reads the
// token from the environment, so CI can validate a draft while public clients
// simply cannot see it.
type GitHubSource struct {
	Owner string
	Repo  string
	Token string
	HTTP  *http.Client
	// APIBase is overridable for tests and GitHub Enterprise.
	APIBase string
}

func (g *GitHubSource) Name() string { return "github" }

func (g *GitHubSource) base() string {
	if g.APIBase != "" {
		return g.APIBase
	}
	return "https://api.github.com"
}

func (g *GitHubSource) headers(accept string) http.Header {
	h := http.Header{}
	h.Set("Accept", accept)
	h.Set("X-GitHub-Api-Version", "2022-11-28")
	h.Set("User-Agent", "go-selfupdate-template")
	if g.Token != "" {
		h.Set("Authorization", "Bearer "+g.Token)
	}
	return h
}

type ghAsset struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	BrowserURL  string `json:"browser_download_url"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	CreatedAt   time.Time `json:"created_at"`
	Assets      []ghAsset `json:"assets"`
}

func (g *GitHubSource) List(ctx context.Context, opt ListOptions) (ListResult, error) {
	if opt.IncludeDraft && g.Token == "" {
		return ListResult{}, fmt.Errorf("the internal channel needs a token: set APP_UPDATE_TOKEN (it is never embedded in a released binary)")
	}
	limit := opt.Limit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d", g.base(), g.Owner, g.Repo, limit)

	h := g.headers("application/vnd.github+json")
	if opt.ETag != "" {
		h.Set("If-None-Match", opt.ETag)
	}
	res, err := doGet(ctx, g.HTTP, url, h)
	if err != nil {
		return ListResult{}, fmt.Errorf("github: %w", err)
	}
	if res.NotModified {
		return ListResult{NotModified: true, ETag: opt.ETag}, nil
	}

	var raw []ghRelease
	if err := json.Unmarshal(res.Body, &raw); err != nil {
		return ListResult{}, fmt.Errorf("github: parse releases: %w", err)
	}

	out := ListResult{ETag: res.ETag}
	for _, r := range raw {
		if r.Draft && !opt.IncludeDraft {
			continue
		}
		if r.Prerelease && !opt.IncludePrerelease {
			continue
		}
		v, err := version.Parse(r.TagName)
		if err != nil {
			// Tags that are not ours (docs, legacy schemes) are skipped rather
			// than treated as a hard error.
			continue
		}
		rel := Release{
			Version:     v,
			Source:      g.Name(),
			Tag:         r.TagName,
			Draft:       r.Draft,
			Prerelease:  r.Prerelease,
			Notes:       r.Body,
			PublishedAt: firstNonZeroTime(r.PublishedAt, r.CreatedAt),
		}
		for _, a := range r.Assets {
			goos, goarch, _ := classifyAsset(a.Name)
			rel.Assets = append(rel.Assets, Asset{
				Name:   a.Name,
				URL:    a.BrowserURL,
				APIURL: a.URL,
				Size:   a.Size,
				OS:     goos,
				Arch:   goarch,
			})
		}
		out.Releases = append(out.Releases, rel)
	}
	return out, nil
}

func (g *GitHubSource) Fetch(ctx context.Context, a Asset, w io.Writer, offset int64) error {
	// Draft assets have no working browser_download_url, so an authenticated
	// client always goes through the asset API. Go strips the Authorization
	// header on the cross-host redirect to storage, which is what we want.
	url := a.downloadURL(g.Token != "")
	if url == "" {
		return fmt.Errorf("github: asset %q has no download url", a.Name)
	}
	h := g.headers("application/octet-stream")
	if offset > 0 {
		h.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	return streamTo(ctx, g.HTTP, url, h, w, offset)
}

func firstNonZeroTime(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}
