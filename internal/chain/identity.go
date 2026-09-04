// Package chain is the application's single seam onto go-arcade-toolbox.
//
// Everything else in the program depends on this package's own types, never on
// the toolbox directly. That boundary is what made the sibling applications'
// migration between toolbox versions a one-file change, and it is also what
// lets the tag lifecycle be tested without a wallet, a database or a network.
package chain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// keysFileName is where every secret this program holds lives.
const keysFileName = "keys.json"

// Identity is the complete set of secrets a deployment owns.
//
// Losing this file loses the program: the wallet's coins, the ability to
// co-sign a redemption, and every tag key that was ever printed. Leaking it
// loses the same things to somebody else. It is written 0600 inside a 0700
// directory, and both .gitignore and .dockerignore carry overlapping patterns
// for it -- rule-110-arcade published live keys once because a /data/-only
// ignore rule did not match a directory called data-sqlite-backup.
type Identity struct {
	// WalletKeyHex funds activations, pays fees and receives change.
	WalletKeyHex string `json:"wallet_key"`

	// CoSignKeyHex is DNR's half of every tag lock.
	//
	// It is deliberately not the wallet key. The wallet key is handled by the
	// toolbox, passed to constructors, and derived from all over the place; the
	// co-signing key does exactly one thing, so keeping it separate means an
	// accident involving one is not automatically an accident involving both.
	CoSignKeyHex string `json:"cosign_key"`

	// TagSeedHex derives every tag's bearer secret.
	//
	// This is the most dangerous value in the file. Whoever holds it can spend
	// any tag that has ever been printed. It is also what makes a lost print
	// sheet survivable and lets rewards on tags that were never recaptured be
	// swept rather than stranded on chain forever -- and since crabs die and
	// tags are shed at every moult, a real fraction of any batch never returns.
	TagSeedHex string `json:"tag_seed"`

	// DerivationPrefix and DerivationSuffix pin the wallet's BRC-29 deposit
	// address so it survives restarts. A moved deposit address is a silent
	// failure: money sent to the old one is simply not seen.
	DerivationPrefix string `json:"derivation_prefix"`
	DerivationSuffix string `json:"derivation_suffix"`
}

var (
	ErrNoIdentity     = errors.New("chain: no identity file")
	ErrBrokenIdentity = errors.New("chain: identity file is unusable")
)

// LoadIdentity reads the identity from a data directory.
//
// It never creates one. Minting keys as a side effect of failing to find them
// is how a deployment silently becomes a different deployment: a wallet with a
// new identity comes up healthy, reports a zero balance that is technically
// correct, and quietly abandons every coin the old one held. Creation is an
// explicit command.
func LoadIdentity(dataDir string) (*Identity, error) {
	path := filepath.Join(dataDir, keysFileName)
	raw, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied configuration
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNoIdentity, path)
	}
	if err != nil {
		return nil, fmt.Errorf("chain: read %s: %w", path, err)
	}

	var id Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBrokenIdentity, path, err)
	}
	if err := id.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBrokenIdentity, path, err)
	}
	return &id, nil
}

// CreateIdentity mints a fresh identity and writes it, refusing to overwrite.
func CreateIdentity(dataDir string) (*Identity, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("chain: create data directory: %w", err)
	}
	// MkdirAll applies its mode only when it actually creates the directory, so
	// an existing data directory keeps whatever permissions it had -- commonly
	// 0755 from a plain mkdir. This directory is about to hold the master seed
	// from which every tag key in the program derives, so tighten it rather
	// than assume. Failing here is correct: we have just failed to protect the
	// most dangerous secret this program owns, and continuing quietly would
	// leave it readable with nobody aware of it.
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("chain: secure data directory %s: %w", dataDir, err)
	}
	path := filepath.Join(dataDir, keysFileName)
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("chain: %s already exists; refusing to overwrite live keys", path)
	}

	walletKey, err := ec.NewPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("chain: generate wallet key: %w", err)
	}
	coSignKey, err := ec.NewPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("chain: generate co-signing key: %w", err)
	}
	seed, err := tagkey.NewSeed()
	if err != nil {
		return nil, fmt.Errorf("chain: generate tag seed: %w", err)
	}
	prefix, err := randomDerivation()
	if err != nil {
		return nil, err
	}
	suffix, err := randomDerivation()
	if err != nil {
		return nil, err
	}

	id := &Identity{
		WalletKeyHex:     hex.EncodeToString(walletKey.Serialize()),
		CoSignKeyHex:     hex.EncodeToString(coSignKey.Serialize()),
		TagSeedHex:       hex.EncodeToString(seed[:]),
		DerivationPrefix: prefix,
		DerivationSuffix: suffix,
	}
	if err := id.write(path); err != nil {
		return nil, err
	}
	return id, nil
}

// write persists the identity atomically.
//
// Write-then-rename, because a half-written keys.json is indistinguishable from
// a corrupt one and the recovery for both is "restore from backup".
func (id *Identity) write(path string) error {
	raw, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("chain: encode identity: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("chain: write identity: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chain: install identity: %w", err)
	}
	return nil
}

// Validate checks that every secret in the file parses.
func (id *Identity) Validate() error {
	if _, err := id.WalletKey(); err != nil {
		return fmt.Errorf("wallet key: %w", err)
	}
	if _, err := id.CoSignKey(); err != nil {
		return fmt.Errorf("co-signing key: %w", err)
	}
	if _, err := id.TagSeed(); err != nil {
		return fmt.Errorf("tag seed: %w", err)
	}
	if id.DerivationPrefix == "" || id.DerivationSuffix == "" {
		return errors.New("derivation prefix and suffix are required")
	}
	return nil
}

// WalletKey is the toolbox wallet's private key.
func (id *Identity) WalletKey() (*ec.PrivateKey, error) {
	return ec.PrivateKeyFromHex(id.WalletKeyHex) //nolint:wrapcheck // the caller adds the context
}

// CoSignKey is DNR's half of every tag lock.
func (id *Identity) CoSignKey() (*ec.PrivateKey, error) {
	return ec.PrivateKeyFromHex(id.CoSignKeyHex) //nolint:wrapcheck // the caller adds the context
}

// TagSeed derives tag bearer secrets.
func (id *Identity) TagSeed() (tagkey.Seed, error) {
	raw, err := hex.DecodeString(id.TagSeedHex)
	if err != nil {
		return tagkey.Seed{}, fmt.Errorf("decode: %w", err)
	}
	return tagkey.SeedFromBytes(raw)
}

// SecretFor regenerates a tag's bearer secret from its ordinal. This is the
// path a sweep takes, and the path a reprint takes.
func (id *Identity) SecretFor(ordinal uint64) (tagkey.Secret, error) {
	seed, err := id.TagSeed()
	if err != nil {
		return tagkey.Secret{}, err
	}
	return seed.SecretFor(ordinal), nil
}

// WalletIdentityKeyHex is the wallet's public identity key, which the arcade
// callback token is derived from and which crabbers' wallets need in order to
// derive the BRC-29 payment they are being sent.
func (id *Identity) WalletIdentityKeyHex() (string, error) {
	key, err := id.WalletKey()
	if err != nil {
		return "", fmt.Errorf("chain: wallet identity key: %w", err)
	}
	return hex.EncodeToString(key.PubKey().Compressed()), nil
}

// CoSignPubKeyHex is DNR's co-signing public key, which appears in every tag
// locking script and is therefore public by construction.
func (id *Identity) CoSignPubKeyHex() (string, error) {
	key, err := id.CoSignKey()
	if err != nil {
		return "", fmt.Errorf("chain: co-signing public key: %w", err)
	}
	return hex.EncodeToString(key.PubKey().Compressed()), nil
}

// randomDerivation produces a BRC-29 derivation component: 8 random bytes,
// base64. These are not secret -- they travel with every payment remittance --
// but they must be stable, because they determine the deposit address.
func randomDerivation() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("chain: generate derivation component: %w", err)
	}
	return base64Std(b[:]), nil
}
