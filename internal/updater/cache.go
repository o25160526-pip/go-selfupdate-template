package updater

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/o25160526-pip/go-selfupdate-template/internal/version"
)

// Cache stores release metadata and downloaded binaries.
//
// Metadata is cached with a TTL plus the source ETag, so a repeated check is
// either free (inside the TTL) or a 304 (outside it). Binaries are addressed by
// sha256, which makes a cache hit self-verifying and stores a version that
// exists on both sources only once.
type Cache struct {
	Dir       string
	TTL       time.Duration
	KeepBlobs int
}

func NewCache(dir string, ttl time.Duration, keep int) *Cache {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if keep <= 0 {
		keep = 6
	}
	return &Cache{Dir: dir, TTL: ttl, KeepBlobs: keep}
}

type metaFile struct {
	Source    string    `json:"source"`
	Channel   string    `json:"channel"`
	ETag      string    `json:"etag,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Releases  []Release `json:"releases"`
}

// BlobInfo is the sidecar describing a cached binary.
type BlobInfo struct {
	SHA256    string          `json:"sha256"`
	Version   version.Version `json:"version"`
	Asset     string          `json:"asset"`
	Source    string          `json:"source"`
	Size      int64           `json:"size"`
	CreatedAt time.Time       `json:"created_at"`
	LastUsed  time.Time       `json:"last_used"`
}

func (c *Cache) metaDir() string  { return filepath.Join(c.Dir, "meta") }
func (c *Cache) blobsDir() string { return filepath.Join(c.Dir, "blobs") }

func (c *Cache) metaPath(source, channel string) string {
	return filepath.Join(c.metaDir(), fmt.Sprintf("%s-%s.json", safeName(source), safeName(channel)))
}

// LoadReleases returns cached releases, the stored ETag, and whether the entry
// is still inside its TTL.
//
// A stale entry is still returned on purpose: it lets a check succeed while
// offline and it is what enables the conditional request.
func (c *Cache) LoadReleases(source, channel string) (rs []Release, etag string, fresh bool) {
	b, err := os.ReadFile(c.metaPath(source, channel))
	if err != nil {
		return nil, "", false
	}
	var m metaFile
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "", false
	}
	return m.Releases, m.ETag, time.Since(m.FetchedAt) < c.TTL
}

func (c *Cache) SaveReleases(source, channel string, rs []Release, etag string) error {
	if err := os.MkdirAll(c.metaDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(metaFile{
		Source:    source,
		Channel:   channel,
		ETag:      etag,
		FetchedAt: time.Now(),
		Releases:  rs,
	}, "", "  ")
	if err != nil {
		return err
	}
	path := c.metaPath(source, channel)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// TouchMeta refreshes the fetch time after a 304, so a NotModified answer
// resets the TTL instead of re-requesting on every single check.
func (c *Cache) TouchMeta(source, channel string) {
	path := c.metaPath(source, channel)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m metaFile
	if json.Unmarshal(b, &m) != nil {
		return
	}
	m.FetchedAt = time.Now()
	if nb, err := json.MarshalIndent(m, "", "  "); err == nil {
		os.WriteFile(path, nb, 0o600)
	}
}

func validSHA(sha string) bool {
	if len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

// BlobPath is the on-disk path for a digest. Digests are validated so a
// hostile checksums file cannot make us write outside the cache directory.
func (c *Cache) BlobPath(sha string) string {
	sha = strings.ToLower(sha)
	if !validSHA(sha) {
		return filepath.Join(c.blobsDir(), "invalid")
	}
	return filepath.Join(c.blobsDir(), sha)
}

func (c *Cache) HasBlob(sha string) bool {
	if !validSHA(strings.ToLower(sha)) {
		return false
	}
	st, err := os.Stat(c.BlobPath(sha))
	return err == nil && st.Size() > 0
}

func (c *Cache) infoPath(sha string) string { return c.BlobPath(sha) + ".json" }

func (c *Cache) SaveBlobInfo(info BlobInfo) error {
	if !validSHA(strings.ToLower(info.SHA256)) {
		return fmt.Errorf("invalid digest")
	}
	if info.CreatedAt.IsZero() {
		info.CreatedAt = time.Now()
	}
	info.LastUsed = time.Now()
	if st, err := os.Stat(c.BlobPath(info.SHA256)); err == nil {
		info.Size = st.Size()
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.infoPath(info.SHA256), b, 0o600)
}

// Touch records a cache hit for LRU purposes.
func (c *Cache) Touch(sha string) {
	path := c.infoPath(sha)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var info BlobInfo
	if json.Unmarshal(b, &info) != nil {
		return
	}
	info.LastUsed = time.Now()
	if nb, err := json.MarshalIndent(info, "", "  "); err == nil {
		os.WriteFile(path, nb, 0o600)
	}
}

// Blobs lists cached binaries, newest version first.
func (c *Cache) Blobs() ([]BlobInfo, error) {
	entries, err := os.ReadDir(c.blobsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BlobInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.blobsDir(), name))
		if err != nil {
			continue
		}
		var info BlobInfo
		if json.Unmarshal(b, &info) != nil {
			continue
		}
		if !c.HasBlob(info.SHA256) {
			continue // orphaned sidecar
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version.After(out[j].Version) })
	return out, nil
}

// Prune keeps the newest `keep` binaries, dropping the least recently used of
// the rest, and clears .part files that have been abandoned for a day.
func (c *Cache) Prune(keep int) (removed int, err error) {
	if keep <= 0 {
		keep = c.KeepBlobs
	}
	blobs, err := c.Blobs()
	if err != nil {
		return 0, err
	}
	if len(blobs) > keep {
		victims := blobs[keep:]
		sort.Slice(victims, func(i, j int) bool { return victims[i].LastUsed.Before(victims[j].LastUsed) })
		for _, v := range victims {
			os.Remove(c.BlobPath(v.SHA256))
			os.Remove(c.infoPath(v.SHA256))
			removed++
		}
	}
	entries, _ := os.ReadDir(c.blobsDir())
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		info, ierr := e.Info()
		if ierr == nil && time.Since(info.ModTime()) > 24*time.Hour {
			os.Remove(filepath.Join(c.blobsDir(), e.Name()))
			removed++
		}
	}
	return removed, nil
}

// Clear removes everything.
func (c *Cache) Clear() error { return os.RemoveAll(c.Dir) }

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}
