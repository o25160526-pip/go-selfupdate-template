package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Cache stores source metadata and verified binaries on disk.
type Cache struct {
	Dir       string
	KeepBlobs int
}

type CacheEntry struct {
	SHA256    string    `json:"sha256"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	LastUsed  time.Time `json:"last_used"`
	Verified  bool      `json:"verified"`
}

type cacheIndex struct {
	Entries map[string]CacheEntry `json:"entries"`
}

func (c Cache) BlobPath(sha256 string) string {
	return filepath.Join(c.Dir, "blobs", sha256)
}

func (c Cache) PartialPath(sha256 string) string { return c.BlobPath(sha256) + ".part" }

func (c Cache) Ensure() error {
	return os.MkdirAll(filepath.Join(c.Dir, "blobs"), 0o700)
}

// Lookup returns only a complete verified blob and touches its LRU timestamp.
func (c Cache) Lookup(sha256 string) (string, bool) {
	idx, _ := c.load()
	entry, ok := idx.Entries[sha256]
	if !ok || !entry.Verified {
		return "", false
	}
	if _, err := os.Stat(entry.Path); err != nil {
		delete(idx.Entries, sha256)
		_ = c.save(idx)
		return "", false
	}
	entry.LastUsed = time.Now().UTC()
	idx.Entries[sha256] = entry
	_ = c.save(idx)
	return entry.Path, true
}

func (c Cache) Commit(partialPath, sha256 string) (string, error) {
	if err := c.Ensure(); err != nil {
		return "", err
	}
	actual, err := SHA256File(partialPath)
	if err != nil {
		return "", err
	}
	if actual != sha256 {
		return "", fmt.Errorf("download checksum mismatch: got %s want %s", actual, sha256)
	}
	final := c.BlobPath(sha256)
	if err := os.Rename(partialPath, final); err != nil {
		return "", err
	}
	info, err := os.Stat(final)
	if err != nil {
		return "", err
	}
	idx, _ := c.load()
	idx.Entries[sha256] = CacheEntry{SHA256: sha256, Path: final, Size: info.Size(), LastUsed: time.Now().UTC(), Verified: true}
	if err := c.save(idx); err != nil {
		return "", err
	}
	_ = c.Prune()
	return final, nil
}

func (c Cache) Prune() error {
	keep := c.KeepBlobs
	if keep <= 0 {
		keep = 6
	}
	idx, _ := c.load()
	entries := make([]CacheEntry, 0, len(idx.Entries))
	for _, entry := range idx.Entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].LastUsed.After(entries[j].LastUsed) })
	for _, entry := range entries[keep:] {
		_ = os.Remove(entry.Path)
		delete(idx.Entries, entry.SHA256)
	}
	return c.save(idx)
}

func (c Cache) load() (cacheIndex, error) {
	idx := cacheIndex{Entries: map[string]CacheEntry{}}
	body, err := os.ReadFile(filepath.Join(c.Dir, "index.json"))
	if os.IsNotExist(err) {
		return idx, nil
	}
	if err != nil {
		return idx, err
	}
	if err := json.Unmarshal(body, &idx); err != nil {
		return cacheIndex{Entries: map[string]CacheEntry{}}, err
	}
	if idx.Entries == nil {
		idx.Entries = map[string]CacheEntry{}
	}
	return idx, nil
}

func (c Cache) save(idx cacheIndex) error {
	if err := c.Ensure(); err != nil {
		return err
	}
	body, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(c.Dir, "index.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
