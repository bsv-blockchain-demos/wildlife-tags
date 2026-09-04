package chain

import (
	"encoding/hex"
	"fmt"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
)

// tagPrivKey regenerates a tag's bearer key from the master seed.
//
// This is DNR's path to a tag key, used to sweep a reward nobody claimed and to
// reprint a lost batch. A crabber's browser reaches the same key from the other
// direction, deriving it from the secret in the QR fragment, and never asks the
// server for it.
func (c *Chain) tagPrivKey(ordinal uint64) (*ec.PrivateKey, error) {
	secret, err := c.Identity.SecretFor(ordinal)
	if err != nil {
		return nil, fmt.Errorf("chain: derive tag secret: %w", err)
	}
	return secret.PrivateKey(), nil
}

// tagPubKey is the half that appears in a locking script.
func (c *Chain) tagPubKey(ordinal uint64) (*ec.PublicKey, error) {
	priv, err := c.tagPrivKey(ordinal)
	if err != nil {
		return nil, err
	}
	return priv.PubKey(), nil
}

// parsePubHex reads a compressed public key in the hex form identity keys are
// passed around in.
func parsePubHex(s string) (*ec.PublicKey, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	pub, err := ec.PublicKeyFromBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return pub, nil
}
