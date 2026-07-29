package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// Cache stores release metadata and verified binaries on disk.
type Cache struct {
	Dir       string
	TTL       time.Duration
	KeepBlobs int
}

type CacheEntry struct {
	SHA256   string    `json:"sha256"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	LastUsed time.Time `json:"last_used"`
	Verified bool      `json:"verified"`
}

type BlobInfo struct {
	SHA256 string         `json:"sha256"`
	Path   string         `json:"path,omitempty"`
	Size   int64          `json:"size,omitempty"`
	Version version.Version `json:"version"`
	Asset  string         `json:"asset"`
	Source string         `json:"source"`
}

type cacheIndex struct {
	Entries map[string]CacheEntry `json:"entries"`
	Blobs   map[string]BlobInfo   `json:"blobs,omitempty"`
}

type releaseCache struct {
	Releases []Release `json:"releases"`
	ETag     string    `json:"etag,omitempty"`
	Fetched  time.Time `json:"fetched"`
}

func NewCache(dir string, ttl time.Duration, keep int) *Cache { return &Cache{Dir: dir, TTL: ttl, KeepBlobs: keep} }
func (c Cache) BlobPath(sha256 string) string { return filepath.Join(c.Dir, "blobs", sha256) }
func (c Cache) PartialPath(sha256 string) string { return c.BlobPath(sha256) + ".part" }
func (c Cache) metaDir() string { return filepath.Join(c.Dir, "meta") }
func (c Cache) Ensure() error { return os.MkdirAll(filepath.Join(c.Dir, "blobs"), 0o700) }

func (c Cache) HasBlob(sha256 string) bool {
	path := c.BlobPath(sha256)
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func (c Cache) Touch(sha256 string) { idx, _ := c.load(); if e, ok := idx.Entries[sha256]; ok { e.LastUsed = time.Now().UTC(); idx.Entries[sha256] = e; _ = c.save(idx) } }

// Lookup returns only a complete verified blob and touches its LRU timestamp.
func (c Cache) Lookup(sha256 string) (string, bool) {
	if !c.HasBlob(sha256) { return "", false }
	idx, _ := c.load(); entry := idx.Entries[sha256]; entry.SHA256 = sha256; entry.Path = c.BlobPath(sha256); entry.Verified = true; entry.LastUsed = time.Now().UTC(); idx.Entries[sha256] = entry; _ = c.save(idx)
	return entry.Path, true
}

func (c Cache) SaveBlobInfo(info BlobInfo) error {
	idx, _ := c.load(); if idx.Blobs == nil { idx.Blobs = map[string]BlobInfo{} }; if info.Path == "" { info.Path = c.BlobPath(info.SHA256) }; if st, err := os.Stat(info.Path); err == nil { info.Size = st.Size() }; idx.Blobs[info.SHA256] = info; e := idx.Entries[info.SHA256]; e.SHA256, e.Path, e.Size, e.Verified, e.LastUsed = info.SHA256, info.Path, info.Size, true, time.Now().UTC(); idx.Entries[info.SHA256] = e; return c.save(idx)
}

func (c Cache) Commit(partialPath, sha256 string) (string, error) {
	if err := c.Ensure(); err != nil { return "", err }
	actual, err := SHA256File(partialPath); if err != nil { return "", err }
	if actual != sha256 { return "", fmt.Errorf("download checksum mismatch: got %s want %s", actual, sha256) }
	final := c.BlobPath(sha256); if err := os.Rename(partialPath, final); err != nil { return "", err }
	info, err := os.Stat(final); if err != nil { return "", err }
	idx, _ := c.load(); idx.Entries[sha256] = CacheEntry{SHA256: sha256, Path: final, Size: info.Size(), LastUsed: time.Now().UTC(), Verified: true}; if err := c.save(idx); err != nil { return "", err }; _, _ = c.Prune(); return final, nil
}

// Prune removes least-recently-used blobs and returns the number removed.
func (c Cache) Prune(overrides ...int) (int, error) {
	keep := c.KeepBlobs; if len(overrides) > 0 && overrides[0] > 0 { keep = overrides[0] }; if keep <= 0 { keep = 6 }
	idx, err := c.load(); if err != nil { return 0, err }; entries := make([]CacheEntry, 0, len(idx.Entries)); for _, entry := range idx.Entries { entries = append(entries, entry) }; sort.Slice(entries, func(i, j int) bool { return entries[i].LastUsed.After(entries[j].LastUsed) }); if len(entries) <= keep { return 0, c.save(idx) }
	removed := 0; for _, entry := range entries[keep:] { if os.Remove(entry.Path) == nil { removed++ }; delete(idx.Entries, entry.SHA256); delete(idx.Blobs, entry.SHA256) }; return removed, c.save(idx)
}

func (c Cache) LoadReleases(source, channel string) ([]Release, string, bool) {
	path := filepath.Join(c.metaDir(), source+"-"+channel+".json"); body, err := os.ReadFile(path); if err != nil { return nil, "", false }; var v releaseCache; if json.Unmarshal(body, &v) != nil { return nil, "", false }; fresh := c.TTL <= 0 || time.Since(v.Fetched) <= c.TTL; return v.Releases, v.ETag, fresh
}
func (c Cache) SaveReleases(source, channel string, releases []Release, etag string) error { if err := os.MkdirAll(c.metaDir(), 0o700); err != nil { return err }; v := releaseCache{Releases: releases, ETag: etag, Fetched: time.Now().UTC()}; b, err := json.MarshalIndent(v, "", "  "); if err != nil { return err }; return os.WriteFile(filepath.Join(c.metaDir(), source+"-"+channel+".json"), append(b, '\n'), 0o600) }
func (c Cache) TouchMeta(source, channel string) { releases, etag, _ := c.LoadReleases(source, channel); _ = c.SaveReleases(source, channel, releases, etag) }

func (c Cache) load() (cacheIndex, error) { idx := cacheIndex{Entries: map[string]CacheEntry{}, Blobs: map[string]BlobInfo{}}; body, err := os.ReadFile(filepath.Join(c.Dir, "index.json")); if os.IsNotExist(err) { return idx, nil }; if err != nil { return idx, err }; if err := json.Unmarshal(body, &idx); err != nil { return cacheIndex{Entries: map[string]CacheEntry{}, Blobs: map[string]BlobInfo{}}, err }; if idx.Entries == nil { idx.Entries = map[string]CacheEntry{} }; if idx.Blobs == nil { idx.Blobs = map[string]BlobInfo{} }; return idx, nil }
func (c Cache) save(idx cacheIndex) error { if err := c.Ensure(); err != nil { return err }; body, err := json.MarshalIndent(idx, "", "  "); if err != nil { return err }; path := filepath.Join(c.Dir, "index.json"); tmp := path + ".tmp"; if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil { return err }; return os.Rename(tmp, path) }
