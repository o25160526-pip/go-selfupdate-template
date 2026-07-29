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
	in := flag.String("in", "", "file to sign")
	out := flag.String("out", "", "signature output")
	key := flag.String("key", "", "base64 private key (defaults to APP_SIGNING_KEY)")
	flag.Parse()
	if *in == "" || *out == "" { flag.Usage(); os.Exit(2) }
	value := strings.TrimSpace(*key)
	if value == "" { value = strings.TrimSpace(os.Getenv("APP_SIGNING_KEY")) }
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != ed25519.PrivateKeySize { fmt.Fprintln(os.Stderr, "invalid ed25519 private key"); os.Exit(1) }
	body, err := os.ReadFile(*in)
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	sig := ed25519.Sign(ed25519.PrivateKey(raw), body)
	encoded := base64.StdEncoding.EncodeToString(sig) + "\n"
	if err := os.WriteFile(*out, []byte(encoded), 0o600); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}
