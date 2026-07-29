// Command keygen creates an ed25519 signing key pair.
//
// Run it twice when starting a project: once for the primary key and once for
// the rotation key. Both public keys are baked into every build, so losing the
// primary key becomes recoverable instead of bricking every installed client.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	out := flag.String("out", "keys", "directory to write the private key into")
	name := flag.String("name", "signing", "key name; use signing and signing-next")
	flag.Parse()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		fatal("generate key: %v", err)
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		fatal("mkdir %s: %v", *out, err)
	}
	privPath := filepath.Join(*out, *name+".key")
	if _, err := os.Stat(privPath); err == nil {
		fatal("%s already exists: refusing to overwrite a signing key", privPath)
	}
	encPriv := base64.StdEncoding.EncodeToString(priv)
	if err := os.WriteFile(privPath, []byte(encPriv+"\n"), 0o600); err != nil {
		fatal("write %s: %v", privPath, err)
	}
	encPub := base64.StdEncoding.EncodeToString(pub)

	fmt.Println("key pair created")
	fmt.Println()
	fmt.Printf("  private key : %s   (0600, never commit; keys/ is gitignored)\n", privPath)
	fmt.Printf("  public  key : %s\n", encPub)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. store the private key as a CI secret:  gh secret set SIGNING_KEY < %s\n", privPath)
	fmt.Printf("  2. build with the public key:             make build PUBKEY=%s\n", encPub)
	fmt.Println("  3. create the rotation key NOW, not later: go run ./tools/keygen -name signing-next")
	fmt.Println("     then build with PUBKEY_NEXT set to its public key.")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "keygen: "+format+"\n", args...)
	os.Exit(1)
}
