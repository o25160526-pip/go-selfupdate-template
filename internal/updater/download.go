package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ErrChecksum means the bytes on disk do not match the signed digest. Like a
// bad signature this is never retried and never applied.
var ErrChecksum = errors.New("checksum mismatch")

var errRestartDownload = errors.New("server ignored the byte range, restarting download")

// streamTo copies an HTTP body into w, honouring resume semantics.
//
// If we asked for a byte range and the server answered 200 instead of 206, we
// must not append: doing so silently corrupts the file. The download restarts
// from zero instead.
func streamTo(ctx context.Context, c *http.Client, url string, hdr http.Header, w io.Writer, offset int64) error {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusPartialContent:
		// resumed as requested
	case resp.StatusCode == http.StatusOK:
		if offset > 0 {
			return errRestartDownload
		}
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		return errRestartDownload
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: GET %s: %s", ErrUnreachable, url, resp.Status)
	default:
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return nil
}

// Progress reports download progress. total is -1 when unknown.
type Progress func(done, total int64)

// fetchToMemory downloads a small metadata asset such as checksums.txt.
func fetchToMemory(ctx context.Context, src Source, a Asset) ([]byte, error) {
	var buf memWriter
	err := retry(ctx, 3, 500*time.Millisecond, func() error {
		buf.reset()
		return src.Fetch(ctx, a, &buf, 0)
	})
	if err != nil {
		return nil, err
	}
	return buf.b, nil
}

// downloadBlob fetches an asset into the content-addressed cache and verifies
// it against the expected digest before making it visible.
//
// Bytes land in <blob>.part first, so an interrupted transfer can resume and a
// half-written file can never be mistaken for a cached binary.
func (u *Updater) downloadBlob(ctx context.Context, src Source, a Asset, wantSHA string, p Progress) (string, error) {
	if len(wantSHA) != 64 {
		return "", fmt.Errorf("refusing to download %q without a digest from the signed checksums file", a.Name)
	}
	final := u.Cache.BlobPath(wantSHA)
	if u.Cache.HasBlob(wantSHA) {
		u.Cache.Touch(wantSHA)
		return final, nil
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return "", err
	}
	part := final + ".part"

	err := retry(ctx, 4, time.Second, func() error {
		offset := int64(0)
		if st, serr := os.Stat(part); serr == nil {
			offset = st.Size()
		}
		if a.Size > 0 && offset > a.Size {
			// The local file is larger than the published size, so it is junk.
			os.Remove(part)
			offset = 0
		}
		f, ferr := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o600)
		if ferr != nil {
			return ferr
		}
		if _, serr := f.Seek(offset, io.SeekStart); serr != nil {
			f.Close()
			return serr
		}
		var w io.Writer = f
		if p != nil {
			w = &progressWriter{w: f, done: offset, total: a.Size, fn: p}
		}
		fetchErr := src.Fetch(ctx, a, w, offset)
		closeErr := f.Close()
		if errors.Is(fetchErr, errRestartDownload) {
			os.Remove(part)
			return fetchErr
		}
		if fetchErr != nil {
			return fetchErr
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}

	got, err := SHA256File(part)
	if err != nil {
		return "", err
	}
	if got != wantSHA {
		// Never keep corrupt bytes around to be resumed forever.
		os.Remove(part)
		return "", fmt.Errorf("%w for %s: got %s, want %s", ErrChecksum, a.Name, got, wantSHA)
	}
	if err := os.Rename(part, final); err != nil {
		return "", err
	}
	return final, nil
}

type progressWriter struct {
	w     io.Writer
	done  int64
	total int64
	fn    Progress
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.fn != nil {
		total := p.total
		if total == 0 {
			total = -1
		}
		p.fn(p.done, total)
	}
	return n, err
}

type memWriter struct{ b []byte }

func (w *memWriter) Write(b []byte) (int, error) {
	if len(w.b)+len(b) > maxMetadataBytes {
		return 0, fmt.Errorf("metadata asset is larger than %d bytes", maxMetadataBytes)
	}
	w.b = append(w.b, b...)
	return len(b), nil
}

func (w *memWriter) reset() { w.b = w.b[:0] }
