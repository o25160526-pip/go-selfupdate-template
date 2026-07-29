package updater

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// ResolveOptions controls source queries and candidate selection.
type ResolveOptions struct {
	Version           *version.Version
	IncludeDraft      bool
	IncludePrerelease bool
	Limit             int
}

type sourceResult struct {
	name string
	list ListResult
	err  error
}

// Resolve queries every source concurrently. A failed mirror does not stop a
// healthy one; an error is returned only when every source fails.
func Resolve(ctx context.Context, sources []Source, opt ResolveOptions) ([]Candidate, error) {
	if len(sources) == 0 {
		return nil, errors.New("no update sources")
	}
	ch := make(chan sourceResult, len(sources))
	var wg sync.WaitGroup
	for _, source := range sources {
		s := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := s.List(ctx, ListOptions{IncludeDraft: opt.IncludeDraft, IncludePrerelease: opt.IncludePrerelease, Limit: opt.Limit})
			ch <- sourceResult{name: s.Name(), list: list, err: err}
		}()
	}
	wg.Wait()
	close(ch)

	merged := map[int64]*Candidate{}
	var failures []error
	succeeded := 0
	for result := range ch {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", result.name, result.err))
			continue
		}
		succeeded++
		for _, release := range result.list.Releases {
			if opt.Version != nil && !release.Version.Equal(*opt.Version) {
				continue
			}
			candidate := merged[release.Version.Key()]
			if candidate == nil {
				candidate = &Candidate{Version: release.Version}
				merged[release.Version.Key()] = candidate
			}
			candidate.add(release)
		}
	}
	if succeeded == 0 {
		return nil, fmt.Errorf("all update sources failed: %w", errors.Join(failures...))
	}

	out := make([]Candidate, 0, len(merged))
	for _, candidate := range merged {
		out = append(out, *candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version.After(out[j].Version) })
	return out, nil
}

// Newer returns only versions newer than current and allowed by policy.
func Newer(candidates []Candidate, current version.Version, manifest *Manifest, channel string, machineBucket int) []Candidate {
	policy, hasPolicy := manifest.Policy(channel)
	out := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if !c.Version.After(current) || manifest.IsBlocked(c.Version) {
			continue
		}
		if hasPolicy {
			if policy.Paused || machineBucket >= policy.Rollout() {
				continue
			}
			allowed := false
			for source := range c.BySource {
				if policy.AllowsSource(source) {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}
