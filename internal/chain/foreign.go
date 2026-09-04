package chain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/arcade"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// BroadcastForeign submits a transaction somebody else's wallet built.
//
// This is the funding path. A BRC-100 wallet asked to pay the program with
// noSend set builds and signs the transaction but does not send it, handing
// back the bytes instead -- and that is deliberate rather than incidental:
// BRC-100's getNetwork answers only "testnet", which does not distinguish one
// teranode test network from another. A wallet left to broadcast on its own may
// send a perfectly valid transaction to a chain this program is not watching,
// where it is invisible: our arcade has no record of it, cannot produce a
// merkle proof for it, and the money is unusable even though it was really
// spent.
//
// Broadcasting it through our own arcade removes the guess. The arcade that
// accepts it is the arcade that will report its status and eventually its
// proof.
func (c *Chain) BroadcastForeign(ctx context.Context, beefOrRaw []byte) (string, error) {
	tx, err := parseFundingTx(beefOrRaw)
	if err != nil {
		return "", err
	}
	txid := tx.TxID().String()

	// Extended Format carries each input's source output alongside it, which is
	// what lets arcade validate without looking anything up. It is only
	// available because the wallet handed back BEEF with the ancestry attached.
	ef, err := tx.EF()
	if err != nil {
		return "", fmt.Errorf("chain: build extended format for %s (the wallet's response may not carry input ancestry): %w", txid, err)
	}

	res, err := c.oracle.Broadcast(ctx, txid, ef)
	if err != nil {
		var bp *arcade.BackpressureError
		if errors.As(err, &bp) {
			return "", fmt.Errorf("chain: arcade is under load, retry after %s: %w", bp.RetryAfter, err)
		}
		return "", fmt.Errorf("chain: broadcast %s: %w", txid, err)
	}
	// A transaction-level rejection comes back with a nil error and this flag
	// set. Code that only checks err treats a permanent 4xx as success, which
	// is the single easiest way to lose a funding transaction.
	if res.Rejected {
		return "", fmt.Errorf("chain: arcade rejected %s: %s", txid, res.ExtraInfo)
	}
	c.logger.Info("broadcast funding transaction", "txid", txid, "status", res.Status)
	return txid, nil
}

// WaitForProof polls until a transaction has a merkle proof, or the context
// ends.
//
// Funding cannot be internalized before this: InternalizeAction verifies the
// whole BEEF, proof included, against locally held headers. That verification
// is the point -- the wallet is not taking anyone's word that this money is
// real -- but it does mean waiting for a block.
func (c *Chain) WaitForProof(ctx context.Context, txid string, every time.Duration, notify func(arcade.Status)) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		rec, err := c.oracle.GetTx(ctx, txid)
		switch {
		case err == nil && len(rec.MerklePath) > 0:
			return nil
		case err == nil:
			if notify != nil {
				notify(rec.Status)
			}
			if rec.Status == arcade.StatusRejected || rec.Status == arcade.StatusDoubleSpendAttempted {
				return fmt.Errorf("chain: %s was %s: %s", txid, rec.Status, rec.ExtraInfo)
			}
		case errors.Is(err, arcade.ErrTxNotFound):
			// Arcade has not caught up with its own intake yet; keep waiting.
			if notify != nil {
				notify("unknown")
			}
		default:
			return fmt.Errorf("chain: poll %s: %w", txid, err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("chain: gave up waiting for a proof for %s: %w", txid, ctx.Err())
		case <-ticker.C:
		}
	}
}

// FundFromForeign is the whole funding path: broadcast, wait for the proof,
// then credit the wallet.
func (c *Chain) FundFromForeign(ctx context.Context, beefOrRaw []byte, poll time.Duration, notify func(arcade.Status)) (string, uint64, error) {
	txid, err := c.BroadcastForeign(ctx, beefOrRaw)
	if err != nil {
		return "", 0, err
	}
	if err := c.WaitForProof(ctx, txid, poll, notify); err != nil {
		return txid, 0, err
	}

	tx, err := parseFundingTx(beefOrRaw)
	if err != nil {
		return txid, 0, err
	}
	total, err := c.ImportFunding(ctx, tx.Bytes(), "")
	if err != nil {
		return txid, 0, err
	}
	return txid, total, nil
}

// parseFundingTx accepts whatever shape a wallet handed back.
func parseFundingTx(b []byte) (*transaction.Transaction, error) {
	if tx, err := transaction.NewTransactionFromBEEF(b); err == nil {
		return tx, nil
	}
	tx, err := transaction.NewTransactionFromBytes(b)
	if err != nil {
		return nil, fmt.Errorf("chain: could not read the funding transaction as BEEF or raw bytes: %w", err)
	}
	return tx, nil
}

// ResumeFunding credits a transaction that was already broadcast.
//
// It exists because waiting for a block is the longest part of funding and the
// least reliable to sit through: a network that is not mining can leave the
// wait outlasting any sensible timeout. Re-running the broadcast path would
// submit the same transaction again, so this takes the txid instead, reads the
// transaction back from arcade -- which keeps rawTx alongside the status --
// waits for the proof, and internalizes.
func (c *Chain) ResumeFunding(ctx context.Context, txid string, poll time.Duration, notify func(arcade.Status)) (uint64, error) {
	rec, err := c.oracle.GetTx(ctx, txid)
	if err != nil {
		if errors.Is(err, arcade.ErrTxNotFound) {
			return 0, fmt.Errorf("chain: arcade has no record of %s; it was never broadcast through this arcade, so fund it with -beef instead", txid)
		}
		return 0, fmt.Errorf("chain: look up %s: %w", txid, err)
	}
	if len(rec.RawTx) == 0 {
		return 0, fmt.Errorf("chain: arcade knows %s but is not holding its bytes; fund it with -beef instead", txid)
	}

	if err := c.WaitForProof(ctx, txid, poll, notify); err != nil {
		return 0, err
	}
	total, err := c.ImportFunding(ctx, rec.RawTx, "")
	if err != nil {
		return 0, err
	}
	return total, nil
}
