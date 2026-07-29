// Command sign produces the detached signature clients verify.
//
// Detached rather than embedded: signing raw bytes needs no canonical JSON, so
// there is no serialisation subtlety that can make signer and verifier
// disagree. Every homegrown JSON canonicaliser eventually does.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	in := flag.String("in", "", "file to sign, e.g. dist/checksums.txt")
	out := flag.String("out", "", "signature file to write; defaults to <in>.sig")
	keyFile := flag.String("key-file", "", "base64 private key file; defaults to the SIGNING_KEY env var")
	flag.Parse()

	if *in == "" {
		fatal("-in is required")
	}
	if *out == "" {
		*out = *in + ".sig"
	}

	raw := strings.TrimSpace(os.Getenv("SIGNING_KEY"))
	if *keyFile != "" {
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			fatal("read key file: %v", err)
		}
		raw = strings.TrimSpace(string(b))
	}
	if raw == "" {
		fatal("no private key: set SIGNING_KEY or pass -key-file")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		fatal("private key is not valid base64: %v", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		fatal("private key has %d bytes, want %d", len(keyBytes), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(keyBytes)

	payload, err := os.ReadFile(*in)
	if err != nil {
		fatal("read %s: %v", *in, err)
	}
	sig := ed25519.Sign(priv, payload)
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		fatal("write %s: %v", *out, err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	fmt.Printf("signed %s -> %s\n", *in, *out)
	fmt.Printf("signed with public key: %s\n", base64.StdEncoding.EncodeToString(pub))
	fmt.Println("clients only accept this if the same public key was compiled into them.")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sign: "+format+"\n", args...)
	os.Exit(1)
}
