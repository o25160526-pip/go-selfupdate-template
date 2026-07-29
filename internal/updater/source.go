package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
)

// ListOptions controls what a source should return.
type ListOptions struct {
	IncludeDraft      bool
	IncludePrerelease bool
	Limit             int
	// ETag enables conditional requests, which is what makes a repeated check
	// nearly free.
	ETag string
}

type ListResult struct {
	Releases    []Release
	ETag        string
	NotModified bool
}

// Source is an update origin.
//
// Keeping this interface tiny is the whole reason a third origin (S3, an
// internal mirror, a file share) can be added later without touching the
// updater, the CLI or the tray.
type Source interface {
	Name() string
	List(ctx context.Context, opt ListOptions) (ListResult, error)
	// Fetch streams an asset into w, starting at offset for resumed downloads.
	Fetch(ctx context.Context, a Asset, w io.Writer, offset int64) error
}

// NewSources builds the enabled sources from configuration.
func NewSources(cfg *config.Config, client *http.Client) ([]Source, error) {
	var out []Source
	for _, sc := range cfg.Sources {
		if !sc.Enabled() {
			continue
		}
		switch sc.Type {
		case config.SourceGitHub:
			out = append(out, &GitHubSource{
				Owner: sc.Owner,
				Repo:  sc.Repo,
				Token: config.UpdateToken(),
				HTTP:  client,
			})
		case config.SourceAzure:
			mode := sc.Mode
			if mode == "" {
				mode = config.AzureModeBlob
			}
			out = append(out, &AzureSource{
				Mode:         mode,
				IndexURL:     sc.IndexURL,
				Organization: sc.Organization,
				Project:      sc.Project,
				Feed:         sc.Feed,
				Package:      sc.Package,
				Token:        config.AzureToken(),
				HTTP:         client,
			})
		default:
			return nil, fmt.Errorf("unknown source type %q", sc.Type)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no update source is enabled")
	}
	return out, nil
}
