// Command sign produces the detached signatures the client verifies before installing an
// update.
//
// The signature covers the SHA-256 of the file rather than the file itself: the client
// hashes while it downloads, so it can verify without holding a 30 MB archive in memory.
//
// Usage: SA05_RELEASE_KEY=<base64 ed25519 private key> sign <file>...
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: sign <file>...")
	}
	raw := os.Getenv("SA05_RELEASE_KEY")
	if raw == "" {
		fail("SA05_RELEASE_KEY не задан")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		fail("SA05_RELEASE_KEY повреждён")
	}
	private := ed25519.PrivateKey(key)

	for _, path := range os.Args[1:] {
		digest, err := digestOf(path)
		if err != nil {
			fail(err.Error())
		}
		signature := ed25519.Sign(private, digest)
		target := path + ".sig"
		if err := os.WriteFile(target, []byte(base64.StdEncoding.EncodeToString(signature)), 0o644); err != nil {
			fail(fmt.Sprintf("подпись не записана: %v", err))
		}
		fmt.Printf("подписан %s\n", path)
	}
}

func digestOf(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("файл не открыт: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, fmt.Errorf("файл не прочитан: %w", err)
	}
	return hash.Sum(nil), nil
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "sign: "+message)
	os.Exit(1)
}
