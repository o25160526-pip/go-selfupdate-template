package updater

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// ErrSignature is returned for every signature failure. Callers map it to
// exit code 30, which is a hard failure: a bad signature is never retried.
var ErrSignature = errors.New("signature verification failed")

// VerifySignature accepts the payload if ANY trusted key validates it.
// That is what makes key rotation work: the new key can start signing before
// every client has been updated.
func VerifySignature(payload, sig []byte, keys []ed25519.PublicKey) error {
	if len(keys) == 0 {
		return ErrNoTrustedKeys
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature has %d bytes, want %d", ErrSignature, len(sig), ed25519.SignatureSize)
	}
	for _, k := range keys {
		if ed25519.Verify(k, payload, sig) {
			return nil
		}
	}
	return ErrSignature
}

// DecodeSignature accepts raw 64-byte signatures as well as base64 text,
// with or without surrounding whitespace.
func DecodeSignature(b []byte) ([]byte, error) {
	if len(b) == ed25519.SignatureSize {
		return b, nil
	}
	s := strings.TrimSpace(string(b))
	// Tolerate a minisign-style comment line.
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	sig, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: signature is neither raw nor base64: %v", ErrSignature, err)
	}
	return sig, nil
}

// SHA256Hex hashes a byte slice.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SHA256File hashes a file without loading it into memory.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ParseChecksums reads `sha256sum` / `shasum -a 256` output into a
// filename -> hex digest map. Both the plain and the "*binary" marker are
// accepted, and paths are reduced to their base name.
func ParseChecksums(b []byte) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != 64 {
			continue
		}
		if _, err := hex.DecodeString(sum); err != nil {
			continue
		}
		name := strings.TrimPrefix(strings.Join(fields[1:], " "), "*")
		out[path.Base(name)] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("checksums file contains no usable entries")
	}
	return out, nil
}
