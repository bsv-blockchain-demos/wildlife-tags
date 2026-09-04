package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/arcade"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/chain"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// fakeLedger stands in for the chain.
//
// It does not pretend to build Bitcoin transactions -- tagscript's tests do
// that against the real interpreter. What it models is the shape of the
// answers the service reasons about: which outputs a redemption produces, what
// each is worth, and whether anything reached the network. That is enough to
// drive the escrow across two recaptures and check the money lands where the
// design says it should.
type fakeLedger struct {
	cfg  chain.Config
	seed tagkey.Seed
	key  *ec.PrivateKey

	mu      sync.Mutex
	txCount int
	// aborted records which drafts had their wallet inputs released, so a test
	// can check that abandoning a redemption hands the coins back.
	aborted []string
	// sweptWithOutputs records any outputs a sweep declared. It must stay empty:
	// a named output is not minted as spendable.
	sweptWithOutputs []string

	// failCompleteWith, when set, makes the next CompleteRedemption fail. The
	// error's identity matters: an ErrNotBroadcast failure must unwind
	// cleanly, and anything else must leave the tag claimed.
	failCompleteWith error
	// failPrepare makes PrepareRedemption fail, which must always unwind.
	failPrepare bool
}

func newFakeLedger(cfg chain.Config) *fakeLedger {
	seed, _ := tagkey.SeedFromBytes([]byte("service test seed, 32 bytes long"))
	key, _ := ec.NewPrivateKey()
	return &fakeLedger{cfg: cfg, seed: seed, key: key}
}

func (f *fakeLedger) Config() chain.Config                    { return f.cfg }
func (f *fakeLedger) TagSeed() (tagkey.Seed, error)           { return f.seed, nil }
func (f *fakeLedger) WalletKey() (*ec.PrivateKey, error)      { return f.key, nil }
func (f *fakeLedger) Balance(context.Context) (uint64, error) { return 10_000_000, nil }
func (f *fakeLedger) DepositAddress() (string, error)         { return "mFakeDepositAddress", nil }
func (f *fakeLedger) OnStatus(func([]arcade.TxRecord))        {}

func (f *fakeLedger) SecretFor(ordinal uint64) (tagkey.Secret, error) {
	return f.seed.SecretFor(ordinal), nil
}

func (f *fakeLedger) WalletIdentityKeyHex() (string, error) {
	return hex.EncodeToString(f.key.PubKey().Compressed()), nil
}

func (f *fakeLedger) nextTxID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txCount++
	return fmt.Sprintf("%064x", f.txCount)
}

func (f *fakeLedger) Observation(req chain.ActivateRequest, at time.Time) ([]byte, error) {
	profile, err := req.Profile()
	if err != nil {
		return nil, err
	}
	if err := profile.ValidateTagging(req.Meas, req.Attr); err != nil {
		return nil, err
	}
	return record.Marshal(record.Observation{
		AccCM: int(req.Fix.AccuracyM * 100),
		Attr:  req.Attr,
		LatE7: record.EncodeCoord(req.Fix.Lat),
		LonE7: record.EncodeCoord(req.Fix.Lon),
		Meas:  req.Meas,
		Name:  req.Name,
		Obs:   req.AttestPubHex,
		Sp:    profile.Code,
		TS:    at.UTC().Format(time.RFC3339),
	})
}

func (f *fakeLedger) ActivationSettlement(batchID string) ([]byte, error) {
	return record.Marshal(record.Settlement{
		BaseSat: f.cfg.BaseSatoshis,
		Batch:   batchID,
		BonSat:  f.cfg.BonusSatoshis,
	})
}

func (f *fakeLedger) Activate(_ context.Context, req chain.ActivateRequest, observation []byte) (*chain.ActivateResult, error) {
	settlement, err := f.ActivationSettlement(req.BatchID)
	if err != nil {
		return nil, err
	}
	return &chain.ActivateResult{
		TxID:        f.nextTxID(),
		Vout:        0,
		Satoshis:    f.cfg.TotalReward(),
		Observation: observation,
		Settlement:  settlement,
		SweepAfter:  time.Now().UTC().Add(f.cfg.SweepAfter),
	}, nil
}

func (f *fakeLedger) PrepareRedemption(_ context.Context, req chain.RedeemRequest) (*chain.RedemptionDraft, error) {
	if f.failPrepare {
		return nil, fmt.Errorf("%w: fake refused to build", chain.ErrNotBroadcast)
	}

	split := chain.PayoutSplit{ReporterSats: f.cfg.BaseSatoshis}
	if req.PendingEscrow != nil {
		split.EscrowSats = req.PendingEscrow.EscrowSats
		split.EscrowFor = req.PendingEscrow.EscrowFor
	}
	if req.Disposition() == species.Released {
		split.NextLockSats = f.cfg.BonusSatoshis + f.cfg.BaseSatoshis
	}

	return &chain.RedemptionDraft{
		Reference:         []byte(fmt.Sprintf("ref-%s-%d", req.TagID, req.Generation)),
		TagID:             string(req.TagID),
		Generation:        req.Generation,
		SignableTx:        []byte{0x01},
		InputIndex:        0,
		SourceSatoshis:    req.PrevSatoshis,
		DerivationPrefix:  "cHJlZml4",
		DerivationSuffix:  "c3VmZml4",
		SenderIdentityKey: hex.EncodeToString(f.key.PubKey().Compressed()),
		Split:             split,
		Observation:       req.Observation,
		Settlement:        req.Settlement,
		Retire:            split.NextLockSats == 0,
		NextVout:          nextVoutFor(split),
		ExpiresAt:         time.Now().UTC().Add(10 * time.Minute),
	}, nil
}

// nextVoutFor mirrors the real output layout: payout at zero, then the escrow
// payment if there is one, then the re-locked output.
func nextVoutFor(split chain.PayoutSplit) int {
	if split.NextLockSats == 0 {
		return -1
	}
	if split.EscrowSats > 0 {
		return 2
	}
	return 1
}

func (f *fakeLedger) CompleteRedemption(_ context.Context, draft *chain.RedemptionDraft, tagSig []byte) (*chain.RedeemResult, error) {
	if f.failCompleteWith != nil {
		err := f.failCompleteWith
		f.failCompleteWith = nil
		return nil, err
	}
	if len(tagSig) == 0 {
		return nil, fmt.Errorf("%w: no tag signature", chain.ErrNotBroadcast)
	}

	escrowVout := -1
	if draft.Split.EscrowSats > 0 {
		escrowVout = 1
	}
	return &chain.RedeemResult{
		TxID:         f.nextTxID(),
		AtomicBEEF:   []byte{0x02},
		PayoutVout:   0,
		EscrowVout:   escrowVout,
		NextVout:     draft.NextVout,
		Split:        draft.Split,
		EscrowPrefix: "cHJlZml4",
		EscrowSuffix: "c3VmZml4",
	}, nil
}

func (f *fakeLedger) AbortDraft(_ context.Context, reference []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborted = append(f.aborted, string(reference))
	return nil
}

func (f *fakeLedger) Sweep(_ context.Context, req chain.SweepRequest) (string, error) {
	// chain.Sweep declares no outputs, so there is nothing to record here. The
	// assertion lives in the test; this field exists to make the requirement
	// visible at the seam.
	return f.nextTxID(), nil
}
