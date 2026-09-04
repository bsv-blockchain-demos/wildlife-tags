package chain

import (
	"context"
	"fmt"

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagscript"
)

// SweepRequest names a tag output to reclaim.
type SweepRequest struct {
	TagID        tagkey.ID
	Ordinal      uint64
	PrevTxID     string
	PrevVout     uint32
	PrevSatoshis uint64
}

// Sweep reclaims the reward locked on a tag that was never reported.
//
// This is not a convenience. Blue crabs live two or three years and shed their
// carapace -- and any tag attached to it -- at every moult, so a real fraction
// of every batch is on the seabed within months. Without a sweep those rewards
// stay locked forever, and a program that permanently burns money for every
// crab that dies is a program nobody will fund twice.
//
// It is also the reason tag keys are derived from a master seed rather than
// generated randomly: DNR has to be able to reach a key whose printed copy is
// at the bottom of a creek.
//
// The sweep uses both halves of the two-of-two, exactly like a redemption. DNR
// holds both here, which is precisely the power the design gives it, and the
// sweep date is written into the activation record at tagging time so the
// promise not to exercise it earlier is publicly checkable.
func (c *Chain) Sweep(ctx context.Context, req SweepRequest) (string, error) {
	tagKey, err := c.tagPrivKey(req.Ordinal)
	if err != nil {
		return "", err
	}

	inputBEEF, err := c.inputBEEFFor(ctx, req.PrevTxID)
	if err != nil {
		return "", err
	}
	prevTxID, err := chainhash.NewHashFromHex(req.PrevTxID)
	if err != nil {
		return "", fmt.Errorf("chain: parse previous txid: %w", err)
	}

	no := false
	created, err := c.Wallet.CreateAction(ctx, sdk.CreateActionArgs{
		Description: fmt.Sprintf("sweep expired crab tag %s", req.TagID),
		InputBEEF:   inputBEEF,
		Inputs: []sdk.CreateActionInput{{
			Outpoint:              transaction.Outpoint{Txid: *prevTxID, Index: req.PrevVout},
			InputDescription:      "expired crab tag reward",
			UnlockingScriptLength: tagscript.UnlockingScriptEstimate,
		}},
		// No explicit output. The whole point of a sweep is to put the money
		// back where the wallet can spend it, and a caller-supplied output is
		// exactly what the wallet will NOT mint into its UTXO pool: only rows
		// with change=true are, by construction, because the application is
		// assumed to own the lifecycle of any output it names itself.
		//
		// Naming a P2PKH output paying our own key therefore "recovers" money
		// into a coin the wallet records and cannot spend -- the balance moves
		// and the spendable pool does not. Leaving outputs empty makes the
		// input's value change, which is minted and spendable.
		Labels: []string{AppLabel, "tag:" + string(req.TagID), "sweep"},
		Options: &sdk.CreateActionOptions{
			SignAndProcess:         &no,
			RandomizeOutputs:       &no,
			AcceptDelayedBroadcast: &no,
		},
	}, c.cfg.Originator)
	if err != nil {
		return "", fmt.Errorf("%w: build sweep for %s: %w", ErrNotBroadcast, req.TagID, err)
	}
	if created.SignableTransaction == nil {
		return "", fmt.Errorf("%w: wallet returned no signable transaction", ErrNotBroadcast)
	}

	tx, err := transaction.NewTransactionFromBEEF(created.SignableTransaction.Tx)
	if err != nil {
		return "", fmt.Errorf("%w: parse signable sweep: %w", ErrNotBroadcast, err)
	}

	tagSig, err := tagscript.SignWith(tagKey, tx, 0)
	if err != nil {
		return "", fmt.Errorf("%w: tag signature: %w", ErrNotBroadcast, err)
	}
	dnrSig, err := tagscript.SignWith(c.coSignKey, tx, 0)
	if err != nil {
		return "", fmt.Errorf("%w: co-signature: %w", ErrNotBroadcast, err)
	}
	unlock, err := tagscript.Unlock(dnrSig, tagSig)
	if err != nil {
		return "", fmt.Errorf("%w: assemble unlocking script: %w", ErrNotBroadcast, err)
	}
	tx.Inputs[0].UnlockingScript = unlock

	if err := tagscript.VerifyInput(tx, 0); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotBroadcast, err)
	}

	signed, err := c.Wallet.SignAction(ctx, sdk.SignActionArgs{
		Reference: created.SignableTransaction.Reference,
		Spends:    map[uint32]sdk.SignActionSpend{0: {UnlockingScript: unlock.Bytes()}},
	}, c.cfg.Originator)
	if err != nil {
		return "", fmt.Errorf("chain: broadcast sweep for %s: %w", req.TagID, err)
	}
	return signed.Txid.String(), nil
}
