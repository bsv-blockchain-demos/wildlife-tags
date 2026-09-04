package service

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

// manifest accumulates a batch's tag public keys into a single hash.
//
// Anchoring that hash on chain at print time proves the tag set predates every
// activation in it. Without it, a sceptic could argue that an inconvenient
// recapture came from a tag conjured up afterwards -- and for a dataset meant
// to inform fishery management, being unable to answer that is a real gap.
type manifest struct{ h hash.Hash }

func newManifest() *manifest { return &manifest{h: sha256.New()} }

func (m *manifest) add(pubkey []byte) { _, _ = m.h.Write(pubkey) }

func (m *manifest) sum() string { return hex.EncodeToString(m.h.Sum(nil)) }
