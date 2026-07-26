package update

import (
	"crypto/ed25519"
	"encoding/base64"
)

// releasePublicKey verifies release signatures.
//
// It is compiled into the client on purpose: the update channel is only as trustworthy as
// the key that checks it, and a key fetched at runtime could be replaced by whoever
// replaced the archive. The matching private key lives in the repository's
// SA05_RELEASE_KEY secret and is used by the release workflow.
const releasePublicKey = "G3TWtez1LHxn2nUJJf438Mk/32+TAaL2u9DkdZz0zfA="

// PublicKey returns the release verification key. A build whose key does not decode has
// no usable update channel, and that is preferable to installing something unverified.
func PublicKey() (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(releasePublicKey)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errKeyInvalid
	}
	return ed25519.PublicKey(decoded), nil
}
