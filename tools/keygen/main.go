package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	fmt.Println("PUBLIC_KEY=" + base64.StdEncoding.EncodeToString(pub))
	fmt.Println("PRIVATE_KEY=" + base64.StdEncoding.EncodeToString(priv))
	fmt.Fprintln(os.Stderr, "Store PRIVATE_KEY as a CI secret. Never commit it.")
}
