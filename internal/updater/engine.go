package updater

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/o25160526-pip/go-selfupdate-template/internal/config"
	"github.com/o25160526-pip/go-selfupdate-template/internal/exitcode"
	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

type Engine struct { Config *config.Config; Sources []Source; Cache Cache; Target string }
type UpdateRequest struct { Version *version.Version; CheckOnly bool }
type UpdateResult struct { Previous version.Version; Installed version.Version; Source string; Updated bool }

func (e *Engine) Update(ctx context.Context, req UpdateRequest) (UpdateResult, error) {
	current, err := version.Self(); if err != nil { return UpdateResult{}, exitcode.Wrap(exitcode.Usage, err) }; result := UpdateResult{Previous: current, Installed: current}
	lock, err := AcquireLock(config.StateDir(), 0); if err != nil { return result, exitcode.Wrap(exitcode.Locked, err) }; defer lock.Release()
	includeDraft := e.Config.Channel == config.ChannelInternal; includePrerelease := includeDraft || e.Config.Channel == config.ChannelBeta
	candidates, err := Resolve(ctx, e.Sources, ResolveOptions{Version:req.Version, IncludeDraft:includeDraft, IncludePrerelease:includePrerelease, Limit:100}); if err != nil { return result, exitcode.Wrap(exitcode.Unreachable, err) }
	if len(candidates) == 0 { if req.Version != nil { return result, exitcode.Wrap(exitcode.NotFound, fmt.Errorf("version %s was not found", req.Version)) }; return result, exitcode.Wrap(exitcode.UpToDate, fmt.Errorf("already up to date")) }
	candidate := candidates[0]; if req.Version == nil && !candidate.Version.After(current) { return result, exitcode.Wrap(exitcode.UpToDate, fmt.Errorf("already up to date at %s", current)) }; if req.CheckOnly { result.Installed = candidate.Version; return result, nil }
	source, release, err := e.pickSource(candidate); if err != nil { return result, exitcode.Wrap(exitcode.NotFound, err) }; binary, err := release.AssetFor(e.Config.AssetPrefix, runtime.GOOS, runtime.GOARCH); if err != nil { return result, exitcode.Wrap(exitcode.NotFound, err) }
	checksumsAsset, ok := release.AssetByName(ChecksumsFile); if !ok { return result, exitcode.Wrap(exitcode.VerifyError, fmt.Errorf("release has no %s", ChecksumsFile)) }; checksums, err := fetchBytes(ctx, source, checksumsAsset); if err != nil { return result, exitcode.Wrap(exitcode.Unreachable, err) }; parsed, err := ParseChecksums(checksums); if err != nil { return result, exitcode.Wrap(exitcode.VerifyError, err) }; expected := strings.ToLower(parsed[binary.Name]); if expected == "" { return result, exitcode.Wrap(exitcode.VerifyError, fmt.Errorf("%s is absent from checksums", binary.Name)) }
	if !e.Config.InsecureSkipVerify { sigAsset, ok := release.AssetByName(SignatureFile); if !ok { return result, exitcode.Wrap(exitcode.VerifyError, fmt.Errorf("release has no %s", SignatureFile)) }; rawSig, err := fetchBytes(ctx, source, sigAsset); if err != nil { return result, exitcode.Wrap(exitcode.Unreachable, err) }; sig, err := DecodeSignature(rawSig); if err != nil { return result, exitcode.Wrap(exitcode.VerifyError, err) }; keys, err := TrustedKeys(); if err != nil { return result, exitcode.Wrap(exitcode.VerifyError, err) }; if err := VerifySignature(checksums, sig, keys); err != nil { return result, exitcode.Wrap(exitcode.VerifyError, err) } }
	cached, ok := e.Cache.Lookup(expected); if !ok { if err := e.Cache.Ensure(); err != nil { return result, exitcode.Wrap(exitcode.ApplyError, err) }; partial := e.Cache.PartialPath(expected); file, err := os.Create(partial); if err != nil { return result, exitcode.Wrap(exitcode.ApplyError, err) }; fetchErr := source.Fetch(ctx, binary, file, 0); closeErr := file.Close(); if fetchErr != nil { return result, exitcode.Wrap(exitcode.Unreachable, fetchErr) }; if closeErr != nil { return result, exitcode.Wrap(exitcode.ApplyError, closeErr) }; cached, err = e.Cache.Commit(partial, expected); if err != nil { return result, exitcode.Wrap(exitcode.VerifyError, err) } }
	if err := Apply(cached, e.Target); err != nil { return result, exitcode.Wrap(exitcode.ApplyError, err) }; _, _ = e.Cache.Prune(); result.Installed, result.Source, result.Updated = candidate.Version, source.Name(), true; return result, nil
}
func (e *Engine) pickSource(c Candidate) (Source, Release, error) { for _, source := range e.Sources { if release, ok := c.BySource[source.Name()]; ok { return source, release, nil } }; return nil, Release{}, fmt.Errorf("no configured source offers %s", c.Version) }
func fetchBytes(ctx context.Context, source Source, asset Asset) ([]byte, error) { var out bytes.Buffer; if err := source.Fetch(ctx, asset, &out, 0); err != nil { return nil, err }; return out.Bytes(), nil }
