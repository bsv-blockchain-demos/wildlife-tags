package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/chain"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// RedeemInput is a finder's report, already signed by their wallet.
type RedeemInput struct {
	TagID tagkey.ID

	Lat, Lon  float64
	AccuracyM float64

	// Meas and Attr are the profile's fields. The profile is the tag's, not the
	// caller's: a report naming a different species is refused.
	Meas map[string]int
	Attr map[string]string

	// PayeePubHex is the finder's BRC-100 identity key: where the money goes.
	PayeePubHex string

	// Name is honoured only if the animal has no name yet.
	Name string

	Observation  []byte
	AttestSig    []byte
	AttestPubHex string
}

// RedeemDraft is what the browser needs in order to check the payout and sign.
type RedeemDraft struct {
	Reference string `json:"reference"`
	TagID     string `json:"tag_id"`

	// SignableTxHex is BEEF. The browser reconstructs the transaction from it,
	// checks output zero, and only then signs with the tag key.
	SignableTxHex string `json:"signable_tx"`
	InputIndex    uint32 `json:"input_index"`

	// These three let the browser ask its own wallet to derive the key output
	// zero should be locked to. That derivation is what makes in-browser
	// signing meaningful: without it the page would be signing whatever the
	// server handed it.
	DerivationPrefix  string `json:"derivation_prefix"`
	DerivationSuffix  string `json:"derivation_suffix"`
	SenderIdentityKey string `json:"sender_identity_key"`

	// PayoutIndex is fixed. It is stated here for the page's convenience, but
	// the page must check that index specifically rather than trusting a
	// server-nominated one.
	PayoutIndex  uint32 `json:"payout_index"`
	PayoutSats   uint64 `json:"payout_satoshis"`
	EscrowSats   uint64 `json:"escrow_satoshis"`
	NextLockSats uint64 `json:"next_lock_satoshis"`

	ExpiresAt time.Time `json:"expires_at"`
}

// RedeemReceipt is a completed payment.
type RedeemReceipt struct {
	TxID             string `json:"txid"`
	AtomicBEEFHex    string `json:"atomic_beef"`
	PayoutIndex      uint32 `json:"payout_index"`
	PayoutSats       uint64 `json:"payout_satoshis"`
	DerivationPrefix string `json:"derivation_prefix"`
	DerivationSuffix string `json:"derivation_suffix"`
	SenderIdentity   string `json:"sender_identity_key"`
	Retired          bool   `json:"retired"`
}

// PrepareRedeem claims the tag and builds the unsigned redemption.
//
// The claim comes first and is atomic. Two crabbers pulling the same trap, or
// one crabber double-tapping a button on a bad connection, must not both get as
// far as building a transaction: the chain would reject the second one, but
// only after telling its crabber they were being paid.
func (s *Service) PrepareRedeem(ctx context.Context, in RedeemInput) (*RedeemDraft, error) {
	tag, err := s.store.GetTag(ctx, string(in.TagID))
	if err != nil {
		return nil, err
	}
	switch tag.Status {
	case store.StatusActive:
	case store.StatusCooldown:
		return nil, fmt.Errorf("%w: it can be reported again after %s",
			ErrCooldown, formatWhen(tag.CooldownUntil))
	case store.StatusRedeeming:
		return nil, ErrTagBusy
	default:
		return nil, fmt.Errorf("%w: it is %s", ErrTagNotActive, tag.Status)
	}
	profile, err := s.profileFor(tag)
	if err != nil {
		return nil, err
	}
	settlement, err := s.checkRecapturePayload(ctx, profile, in, tag)
	if err != nil {
		return nil, err
	}

	if err := s.store.Transition(ctx, tag.TagID, store.StatusActive, store.StatusRedeeming); err != nil {
		if errors.Is(err, store.ErrTagNotAvailable) {
			return nil, ErrTagBusy
		}
		return nil, err
	}

	// From here on every failure must put the tag back, or the reward becomes
	// permanently unclaimable.
	release := func() {
		if rerr := s.store.Transition(context.WithoutCancel(ctx), tag.TagID, store.StatusRedeeming, store.StatusActive); rerr != nil {
			s.logger.Error("could not return a tag to service after a failed redemption",
				"tag", tag.TagID, "error", rerr)
		}
	}

	settlementBytes, err := record.Marshal(settlement)
	if err != nil {
		release()
		return nil, err
	}
	req := chain.RedeemRequest{
		TagID:        tagkey.ID(tag.TagID),
		Ordinal:      tag.Ordinal,
		Generation:   tag.Generation,
		PrevTxID:     tag.LiveTxID,
		PrevVout:     tag.LiveVout,
		PrevSatoshis: tag.LiveSatoshis,
		PayeePubHex:  in.PayeePubHex,
		Species:      profile.Code,
		Fix:          species.Fix{Lat: in.Lat, Lon: in.Lon, AccuracyM: in.AccuracyM, At: s.now()},
		Meas:         in.Meas,
		Attr:         in.Attr,
		Name:         in.Name,
		Observation:  in.Observation,
		Settlement:   settlementBytes,
		AttestSig:    in.AttestSig,
		AttestPubHex: in.AttestPubHex,
	}

	// A bonus owed to the previous reporter, released by this recapture --
	// which is the corroboration that they really did put the animal back. The
	// settlement already names it; this is what actually pays it.
	if settlement.EscrowSat > 0 {
		req.PendingEscrow = &chain.PayoutSplit{
			EscrowSats: settlement.EscrowSat,
			EscrowFor:  settlement.EscrowFor,
		}
	}

	draft, err := s.chain.PrepareRedemption(ctx, req)
	if err != nil {
		release()
		return nil, err
	}

	ref := hex.EncodeToString(draft.Reference)
	s.draftMu.Lock()
	s.drafts[ref] = &pendingDraft{
		draft:   draft,
		tagID:   tag.TagID,
		ordinal: tag.Ordinal,
		payee:   in.PayeePubHex,
		name:    in.Name,
		release: species.Disposition(in.Attr[species.DispositionKey]) == species.Released,
	}
	s.draftMu.Unlock()

	return &RedeemDraft{
		Reference:         ref,
		TagID:             tag.TagID,
		SignableTxHex:     hex.EncodeToString(draft.SignableTx),
		InputIndex:        draft.InputIndex,
		DerivationPrefix:  draft.DerivationPrefix,
		DerivationSuffix:  draft.DerivationSuffix,
		SenderIdentityKey: draft.SenderIdentityKey,
		PayoutIndex:       0,
		PayoutSats:        draft.Split.ReporterSats,
		EscrowSats:        draft.Split.EscrowSats,
		NextLockSats:      draft.Split.NextLockSats,
		ExpiresAt:         draft.ExpiresAt,
	}, nil
}

// CompleteRedeem adds DNR's co-signature, broadcasts, and settles the books.
func (s *Service) CompleteRedeem(ctx context.Context, reference string, tagSig []byte) (*RedeemReceipt, error) {
	s.draftMu.Lock()
	pending, ok := s.drafts[reference]
	if ok {
		delete(s.drafts, reference)
	}
	s.draftMu.Unlock()
	if !ok {
		return nil, ErrUnknownDraft
	}

	tag, err := s.store.GetTag(ctx, pending.tagID)
	if err != nil {
		return nil, err
	}
	nextGen := pending.draft.Generation + 1

	// Write-ahead, before anything is signed. Past SignAction the transaction
	// may exist on the network whatever happens here, so a record written
	// afterwards is a record that can be lost while the money moves.
	eventID, err := s.store.BeginEvent(ctx, store.Event{
		TagID:          tag.TagID,
		Generation:     nextGen,
		Kind:           string(record.KindRecapture),
		PayloadJSON:    string(pending.draft.Observation),
		SettlementJSON: string(pending.draft.Settlement),
		AttestPubKey:   pending.payee,
	})
	if err != nil {
		s.returnToService(ctx, tag.TagID)
		return nil, fmt.Errorf("service: record redemption attempt: %w", err)
	}
	pending.eventID = eventID

	res, err := s.chain.CompleteRedemption(ctx, pending.draft, tagSig)
	if err != nil {
		if errors.Is(err, chain.ErrNotBroadcast) {
			// Nothing exists on the network. Unwind cleanly so the crabber can
			// simply try again.
			if rerr := s.store.RetractEvent(ctx, eventID); rerr != nil {
				s.logger.Warn("could not retract a failed redemption record", "tag", tag.TagID, "error", rerr)
			}
			s.abandon(ctx, pending)
			s.returnToService(ctx, tag.TagID)
			return nil, err
		}
		// The transaction may be on the network. The record stays, marked
		// failed, and the tag stays claimed -- the audit is what resolves this,
		// not a guess made here.
		if ferr := s.store.FailEvent(ctx, eventID, err.Error()); ferr != nil {
			s.logger.Error("could not mark a redemption failed", "tag", tag.TagID, "error", ferr)
		}
		return nil, err
	}

	if err := s.store.BroadcastEvent(ctx, eventID, res.TxID, res.PayoutVout, res.Split.ReporterSats); err != nil {
		s.logger.Error("redemption broadcast but not recorded", "tag", tag.TagID, "txid", res.TxID, "error", err)
	}
	s.settle(ctx, tag, pending, res, nextGen)

	return &RedeemReceipt{
		TxID:             res.TxID,
		AtomicBEEFHex:    hex.EncodeToString(res.AtomicBEEF),
		PayoutIndex:      res.PayoutVout,
		PayoutSats:       res.Split.ReporterSats,
		DerivationPrefix: pending.draft.DerivationPrefix,
		DerivationSuffix: pending.draft.DerivationSuffix,
		SenderIdentity:   pending.draft.SenderIdentityKey,
		Retired:          pending.draft.Retire,
	}, nil
}

// settle updates the books after a successful broadcast.
//
// Every failure here is logged rather than returned: the money has moved, and
// telling the crabber their payment failed because a database write did would
// be a lie. The audit reconciles the chain against the database precisely so
// that this path does not have to be perfect.
func (s *Service) settle(ctx context.Context, tag *store.Tag, pending *pendingDraft, res *chain.RedeemResult, nextGen uint32) {
	// The crab now has a name, if this reporter gave it one. Recorded after the
	// broadcast rather than before, because until then nothing happened.
	if name := species.NormalizeName(pending.name); name != "" && tag.AnimalName == "" {
		if err := s.store.NameAnimal(ctx, tag.TagID, name, pending.payee); err != nil {
			s.logger.Warn("could not record the animal's name", "tag", tag.TagID, "error", err)
		}
	}

	// Release the previous reporter's bonus, if this recapture corroborated it.
	if res.Split.EscrowSats > 0 && res.EscrowVout >= 0 {
		if err := s.store.PayEscrow(ctx, tag.TagID, tag.Generation, res.TxID,
			res.EscrowPrefix, res.EscrowSuffix, uint32(res.EscrowVout)); err != nil { //nolint:gosec // bounded by output count
			s.logger.Error("could not mark an escrow paid", "tag", tag.TagID, "txid", res.TxID, "error", err)
		}
	}

	if !pending.release || res.NextVout < 0 {
		if err := s.store.RetireTag(ctx, tag.TagID); err != nil {
			s.logger.Error("could not retire a tag", "tag", tag.TagID, "error", err)
		}
		return
	}

	// This reporter's own bonus, now locked into the output the next finder
	// will spend, and owed to them when that happens.
	if err := s.store.CreateEscrow(ctx, store.Escrow{
		TagID:       tag.TagID,
		Generation:  nextGen,
		Beneficiary: pending.payee,
		Satoshis:    s.chain.Config().BonusSatoshis,
		CreatedAt:   s.now(),
	}); err != nil {
		s.logger.Error("could not record a re-release bonus", "tag", tag.TagID, "error", err)
	}

	cooldown := s.now().Add(s.chain.Config().CooldownFor)
	if err := s.store.AdvanceTag(ctx, tag.TagID, nextGen, res.TxID,
		uint32(res.NextVout), res.Split.NextLockSats, cooldown); err != nil { //nolint:gosec // bounded by output count
		s.logger.Error("could not advance a tag", "tag", tag.TagID, "error", err)
	}
}

// abandon releases the wallet inputs a draft reserved. CreateAction holds real
// coins from the moment it hands back a signable transaction, so a draft that
// is dropped without this leaves the wallet quietly poorer than its balance
// says -- and on a small pool the next redemption fails for want of funds that
// are sitting there reserved.
func (s *Service) abandon(ctx context.Context, pending *pendingDraft) {
	if pending == nil || pending.draft == nil {
		return
	}
	if err := s.chain.AbortDraft(context.WithoutCancel(ctx), pending.draft.Reference); err != nil {
		s.logger.Warn("could not release the wallet inputs a dropped redemption reserved",
			"tag", pending.tagID, "error", err)
	}
}

func (s *Service) returnToService(ctx context.Context, tagID string) {
	if err := s.store.Transition(context.WithoutCancel(ctx), tagID, store.StatusRedeeming, store.StatusActive); err != nil {
		s.logger.Error("could not return a tag to service", "tag", tagID, "error", err)
	}
}

// ExpireDrafts releases tags whose redemption was started and abandoned.
//
// A crabber who loses signal mid-redemption would otherwise leave the tag
// claimed forever, and the reward on it permanently unclaimable.
func (s *Service) ExpireDrafts(ctx context.Context) int {
	now := s.now()

	s.draftMu.Lock()
	var stale []*pendingDraft
	for ref, p := range s.drafts {
		if now.After(p.draft.ExpiresAt) {
			stale = append(stale, p)
			delete(s.drafts, ref)
		}
	}
	s.draftMu.Unlock()

	for _, p := range stale {
		s.logger.Info("releasing an abandoned redemption", "tag", p.tagID)
		s.abandon(ctx, p)
		s.returnToService(ctx, p.tagID)
	}
	return len(stale)
}

// checkRecapturePayload confirms the signed record describes the reported catch,
// and returns the settlement the server will write beside it.
//
// Without this a finder's wallet could sign one record while the database
// stored another, and the on-chain attestation would be evidence for a claim
// nobody made. The position fix in particular is the whole scientific point of
// the exercise, so it must be the one that was signed.
//
// The settlement is built here rather than handed in, because every value in it
// is the server's own: what it is paying, which output it is spending, and how
// far the animal moved. A client that could name those could name a different
// payout.
func (s *Service) checkRecapturePayload(ctx context.Context, profile *species.Profile, in RedeemInput, tag *store.Tag) (*record.Settlement, error) {
	var got record.Observation
	if err := json.Unmarshal(in.Observation, &got); err != nil {
		return nil, fmt.Errorf("%w: unparseable observation: %v", ErrPayloadMismatch, err)
	}

	// A name is permanent and public, so the bytes that were signed have to be
	// the ones that carry it.
	wantName := ""
	if tag.AnimalName == "" {
		wantName = species.NormalizeName(in.Name)
	}
	want := record.Observation{
		AccCM: int(in.AccuracyM * 100),
		Attr:  in.Attr,
		LatE7: record.EncodeCoord(in.Lat),
		LonE7: record.EncodeCoord(in.Lon),
		Meas:  in.Meas,
		Name:  wantName,
		Obs:   in.PayeePubHex,
		Sp:    profile.Code,
		TS:    got.TS,
	}
	if !got.Equal(want) {
		return nil, fmt.Errorf("%w: the signed record does not match the reported catch", ErrPayloadMismatch)
	}
	// The species is inside the signed bytes, and it has to be the one the tag
	// was armed for. A finder reporting a red drum on a blue crab tag is either
	// a mistake or a different animal wearing a recovered tag, and both are
	// findings rather than things to record silently.
	if got.Sp != profile.Code {
		return nil, fmt.Errorf("%w: this tag was armed for %s, and the report names %s",
			ErrPayloadMismatch, profile.Code, got.Sp)
	}
	if err := s.checkFreshness(got.TS); err != nil {
		return nil, err
	}

	prov, err := s.Provenance(ctx, tag.TagID)
	if err != nil {
		return nil, err
	}
	var escrowSats uint64
	var escrowFor string
	if escrow, eerr := s.store.PendingEscrow(ctx, tag.TagID, tag.Generation); eerr == nil {
		escrowSats, escrowFor = escrow.Satoshis, escrow.Beneficiary
	} else if !errors.Is(eerr, store.ErrNotFound) {
		return nil, eerr
	}

	fix, err := got.Fix()
	if err != nil {
		return nil, err
	}
	set := s.settlementFor(prov, tag, fix, in.PayeePubHex, escrowSats, escrowFor, s.queueDelay(got.TS))
	return &set, nil
}

func formatWhen(t *time.Time) string {
	if t == nil {
		return "a review by DNR"
	}
	return t.Format(time.RFC1123)
}

// ReleaseOrphanedRedemptions returns tags that were mid-redemption when the
// process last stopped.
//
// It must run at startup, and it is safe to do so unconditionally: a draft
// exists only in memory, so after a restart there cannot be a live one. Any tag
// still marked as redeeming is therefore held by a redemption that can never be
// completed -- the wallet's input reservations went with the old process too --
// and without this its reward is unclaimable forever.
//
// The alternative, persisting drafts, would be worse: a restored draft would
// reference wallet inputs that no longer exist, so it could not be signed
// either, and the tag would stay stuck with a more convincing excuse.
func (s *Service) ReleaseOrphanedRedemptions(ctx context.Context) (int, error) {
	stuck, err := s.store.ListTags(ctx, store.StatusRedeeming, 1000, 0)
	if err != nil {
		return 0, err
	}
	released := 0
	for _, tag := range stuck {
		if err := s.store.Transition(ctx, tag.TagID, store.StatusRedeeming, store.StatusActive); err != nil {
			s.logger.Error("could not release a tag orphaned by a restart", "tag", tag.TagID, "error", err)
			continue
		}
		s.logger.Info("released a tag orphaned by a restart", "tag", tag.TagID)
		released++
	}
	return released, nil
}
