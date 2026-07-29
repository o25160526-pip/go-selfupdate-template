package updater

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
)

// Public keys are injected at build time:
//
//	-ldflags "-X ...updater.PublicKeyPrimary=<base64> -X ...updater.PublicKeyNext=<base64>"
//
// Two slots exist from the very first release on purpose. Without a second
// pre-published key, losing the private key bricks every installed client:
// they would all need a manual reinstall. It costs nothing now and cannot be
// added later, because old clients only trust what was compiled into them.
var (
	PublicKeyPrimary string
	PublicKeyNext    string
)

// ErrNoTrustedKeys means the template has not been initialised with a key pair.
var ErrNoTrustedKeys = errors.New("no signing key is embedded in this build: run `make keygen` and pass PUBKEY=... at build time, or set insecure_skip_verify for local development only")

// TrustedKeys returns every key this binary accepts signatures from.
func TrustedKeys() ([]ed25519.PublicKey, error) {
	return ParsePublicKeys(PublicKeyPrimary, PublicKeyNext)
}

// ParsePublicKeys decodes base64 ed25519 public keys, skipping empty entries.
func ParsePublicKeys(vals ...string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for i, v := range vals {
		if v == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("public key %d is not valid base64: %w", i, err)
		}
		if len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("public key %d has %d bytes, want %d", i, len(b), ed25519.PublicKeySize)
		}
		keys = append(keys, ed25519.PublicKey(b))
	}
	return keys, nil
}
