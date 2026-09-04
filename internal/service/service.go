// Package service is the tagging program itself: the rules that decide when a
// tag may be armed, redeemed, re-armed or reclaimed, and the order in which the
// chain and the database are allowed to learn about it.
//
// It sits between internal/chain (which knows about transactions) and
// internal/web (which knows about HTTP), and it exists because the ordering
// rules are the part most likely to be got wrong: a redemption that writes its
// database record after signing loses money on a crash, and one that claims a
// tag without releasing it on failure loses the tag.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/chain"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

var (
	// ErrTagBusy means another redemption already holds this tag.
	ErrTagBusy = errors.New("service: this tag is already being redeemed")
	// ErrTagNotActive means the tag has no live reward to claim.
	ErrTagNotActive = errors.New("service: this tag has no reward to claim")
	// ErrCooldown means the tag was reported recently and is not back in
	// service yet.
	ErrCooldown = errors.New("service: this tag was reported recently and is not back in service")
	// ErrUnknownDraft means the redemption draft expired or never existed.
	ErrUnknownDraft = errors.New("service: this redemption has expired; scan the tag again")
	// ErrPayloadMismatch means the submitted record does not match the
	// submitted fields.
	ErrPayloadMismatch = errors.New("service: the signed record does not match the reported details")
)

// Service is the tagging program.
type Service struct {
	chain  Ledger
	store  *store.Store
	logger *slog.Logger
	now    func() time.Time

	// drafts holds redemptions between prepare and complete.
	//
	// In memory on purpose. A draft pins a tag out of service and reserves
	// wallet inputs, so it should not survive a restart: after a restart the
	// wallet's reservations are gone anyway, and a draft that outlived them
	// would hold a tag hostage against a transaction that can never be signed.
	draftMu sync.Mutex
	drafts  map[string]*pendingDraft
}

type pendingDraft struct {
	draft   *chain.RedemptionDraft
	tagID   string
	ordinal uint64
	eventID int64
	payee   string
	// name is set only when this report is the one naming the animal.
	name    string
	release bool
}

// New builds the service.
func New(c Ledger, s *store.Store, logger *slog.Logger) *Service {
	return &Service{
		chain:  c,
		store:  s,
		logger: logger,
		now:    func() time.Time { return time.Now().UTC() },
		drafts: make(map[string]*pendingDraft),
	}
}

// Chain exposes the ledger for read-only callers such as the funding page.
func (s *Service) Chain() Ledger { return s.chain }

// Store exposes the database for read-only callers.
func (s *Service) Store() *store.Store { return s.store }

// Config is the running configuration.
func (s *Service) Config() chain.Config { return s.chain.Config() }

// MintBatch creates a run of tags.
//
// Nothing touches the chain here. A minted tag is a printed label with a key
// behind it and no money; arming it is a separate, deliberate act performed in
// the field with the animal in hand.
//
// speciesCode names what the run is printed for. It is a hint rather than a
// commitment -- what each animal actually was is decided at activation and
// recorded on chain -- but it is worth carrying, because it decides the QR
// density the printed tag can bear and it is what the tagging form defaults to.
func (s *Service) MintBatch(ctx context.Context, count int, speciesCode, createdBy string) (*store.Batch, []store.Tag, error) {
	if count <= 0 || count > 5000 {
		return nil, nil, fmt.Errorf("service: batch size %d is out of range 1-5000", count)
	}
	if speciesCode == "" {
		speciesCode = species.Default
	}
	profile, err := species.Get(speciesCode)
	if err != nil {
		return nil, nil, err
	}

	first, err := s.store.NextOrdinal(ctx)
	if err != nil {
		return nil, nil, err
	}
	seed, err := s.chain.TagSeed()
	if err != nil {
		return nil, nil, err
	}

	now := s.now()
	batchID, err := newBatchID(now)
	if err != nil {
		return nil, nil, err
	}

	tags := make([]store.Tag, count)
	manifest := newManifest()
	for i := range tags {
		ordinal := first + uint64(i)
		secret := seed.SecretFor(ordinal)
		pub := secret.PrivateKey().PubKey()
		tags[i] = store.Tag{
			TagID:     string(secret.ID()),
			BatchID:   batchID,
			Ordinal:   ordinal,
			PubKeyHex: hex.EncodeToString(pub.Compressed()),
			Status:    store.StatusMinted,
			CreatedAt: now,
		}
		manifest.add(pub.Compressed())
	}

	batch := store.Batch{
		ID:           batchID,
		CreatedAt:    now,
		CreatedBy:    createdBy,
		TagCount:     count,
		FirstOrdinal: first,
		// The manifest hash lets DNR prove the tag set existed before any of
		// them were armed, which forecloses the accusation that a tag was
		// invented after an interesting recapture.
		ManifestHash: manifest.sum(),
		Species:      profile.Code,
	}
	if err := s.store.CreateBatch(ctx, batch); err != nil {
		return nil, nil, fmt.Errorf("service: create batch %s: %w", batchID, err)
	}
	if err := s.store.InsertTags(ctx, tags); err != nil {
		// The most likely cause is two mints racing: both read the same next
		// ordinal, and the unique index on tags.ordinal refuses the loser.
		// That refusal is correct -- two tags sharing an ordinal share a
		// spending key, and the second one printed could spend the first one's
		// reward -- but the raw constraint error says none of that.
		return nil, nil, fmt.Errorf(
			"service: could not create batch %s starting at ordinal %d; another batch may have been created at the same moment, so try again: %w",
			batchID, first, err)
	}
	if err := s.store.Audit(ctx, createdBy, "batch.mint",
		fmt.Sprintf("%s: %d %s tags from ordinal %d", batchID, count, profile.Code, first)); err != nil {
		s.logger.Warn("could not record batch mint in the audit log", "batch", batchID, "error", err)
	}
	return &batch, tags, nil
}

// SecretFor regenerates a tag's bearer secret. It is the print path and the
// sweep path, and it is the only reason a lost print sheet is survivable.
func (s *Service) SecretFor(ordinal uint64) (tagkey.Secret, error) {
	return s.chain.SecretFor(ordinal)
}

// QRPayload is what gets printed into a tag's QR code.
func (s *Service) QRPayload(id tagkey.ID, secret tagkey.Secret) string {
	return tagkey.QRPayload(s.chain.Config().PublicURL, id, secret)
}

// newBatchID names a print run.
//
// The timestamp is for humans -- an operator holding a sheet of tags wants to
// know when it was printed. The random suffix is because seconds are not
// unique enough: an admin double-clicking "create batch" produces two mints in
// the same second, and without it the second one fails on a primary key
// collision with an error that explains nothing.
func newBatchID(now time.Time) (string, error) {
	var raw [3]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("service: generate batch id: %w", err)
	}
	return fmt.Sprintf("B%s-%s", now.Format("20060102"), strings.ToUpper(hex.EncodeToString(raw[:]))), nil
}
