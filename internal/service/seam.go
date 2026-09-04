package service

import (
	"context"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/arcade"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/chain"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// Ledger is everything this package needs from the chain.
//
// It is declared here, at the consumer, rather than imported from
// internal/chain -- the same discipline the sibling applications use. The
// payoff is concrete: the escrow mechanism is the most novel and least
// obvious part of this program, and money moves through it in a specific
// order across two separate recaptures weeks apart. Being able to drive that
// whole sequence against a fake, with no wallet and no network, is the
// difference between believing it works and knowing it does.
type Ledger interface {
	Config() chain.Config
	TagSeed() (tagkey.Seed, error)
	SecretFor(ordinal uint64) (tagkey.Secret, error)
	WalletIdentityKeyHex() (string, error)
	WalletKey() (*ec.PrivateKey, error)
	DepositAddress() (string, error)
	Balance(ctx context.Context) (uint64, error)

	Observation(req chain.ActivateRequest, at time.Time) ([]byte, error)
	ActivationSettlement(batchID string) ([]byte, error)
	Activate(ctx context.Context, req chain.ActivateRequest, observation []byte) (*chain.ActivateResult, error)

	PrepareRedemption(ctx context.Context, req chain.RedeemRequest) (*chain.RedemptionDraft, error)
	CompleteRedemption(ctx context.Context, draft *chain.RedemptionDraft, tagSig []byte) (*chain.RedeemResult, error)
	AbortDraft(ctx context.Context, reference []byte) error

	Sweep(ctx context.Context, req chain.SweepRequest) (string, error)

	OnStatus(fn func([]arcade.TxRecord))
}

// Compile-time proof that the real chain satisfies the seam. Without it a
// method signature could drift and only fail at the one call site that binds
// them, which is main.
var _ Ledger = (*chain.Chain)(nil)
