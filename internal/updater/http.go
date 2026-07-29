package updater

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"time"
)

// maxMetadataBytes caps metadata responses (release lists, manifests,
// checksums) so a hostile or broken server cannot exhaust memory.
const maxMetadataBytes = 16 << 20

// ErrUnreachable marks network-level failures, which map to exit code 50.
var ErrUnreachable = errors.New("source unreachable")

// NewHTTPClient returns a client that honours HTTP_PROXY/HTTPS_PROXY/NO_PROXY
// through http.ProxyFromEnvironment, which corporate networks require.
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:          8,
			ForceAttemptHTTP2:     true,
		},
	}
}

type httpResult struct {
	Body        []byte
	ETag        string
	NotModified bool
}

func doGet(ctx context.Context, c *http.Client, url string, hdr http.Header) (httpResult, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return httpResult{}, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		return httpResult{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return httpResult{NotModified: true, ETag: resp.Header.Get("ETag")}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		httpErr := fmt.Errorf("GET %s: %s: %s", url, resp.Status, bytes.TrimSpace(snippet))
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return httpResult{}, fmt.Errorf("%w: %v", ErrUnreachable, httpErr)
		}
		return httpResult{}, httpErr
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes+1))
	if err != nil {
		return httpResult{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if len(body) > maxMetadataBytes {
		return httpResult{}, fmt.Errorf("GET %s: response larger than %d bytes", url, maxMetadataBytes)
	}
	return httpResult{Body: body, ETag: resp.Header.Get("ETag")}, nil
}

func getBytes(ctx context.Context, c *http.Client, url string, hdr http.Header) ([]byte, error) {
	res, err := doGet(ctx, c, url, hdr)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// retry runs fn with exponential backoff, but only for retryable failures.
// A bad signature or a 404 is never retried: repeating it cannot help.
func retry(ctx context.Context, attempts int, base time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if !retryable(err) || i == attempts-1 {
			return err
		}
		wait := time.Duration(float64(base) * math.Pow(2, float64(i)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return err
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSignature) || errors.Is(err, ErrChecksum) || errors.Is(err, context.Canceled) {
		return false
	}
	return errors.Is(err, ErrUnreachable) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, errRestartDownload)
}
