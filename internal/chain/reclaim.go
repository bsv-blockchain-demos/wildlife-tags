package chain

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib" // postgres, when the wallet is on it
	_ "modernc.org/sqlite"             // pure-Go sqlite, matching the provider

	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
)

// StrandedOutput is an output the wallet recorded but never minted into its
// spendable pool.
type StrandedOutput struct {
	TxID        string
	Vout        uint32
	Satoshis    uint64
	Description string
}

// FindStranded lists outputs paying this wallet's own key that it cannot spend.
//
// The wallet mints only change into its UTXO pool; an output the application
// named itself is assumed to be the application's to manage, and is recorded
// without ever becoming spendable. That is the right default -- it is what
// keeps tag outputs out of the funding pool -- but it means any output an
// application pays to itself by naming it is money the wallet can see and not
// touch.
//
// This finds those, so `reclaim` can put them back.
func (c *Chain) FindStranded(ctx context.Context) ([]StrandedOutput, error) {
	walletKey, err := c.Identity.WalletKey()
	if err != nil {
		return nil, err
	}
	addr, err := script.NewAddressFromPublicKey(walletKey.PubKey(), c.cfg.Mainnet())
	if err != nil {
		return nil, fmt.Errorf("chain: wallet address: %w", err)
	}
	lock, err := p2pkh.Lock(addr)
	if err != nil {
		return nil, fmt.Errorf("chain: wallet locking script: %w", err)
	}
	// The column holds raw script bytes, not hex. Comparing against the hex form
	// silently matches nothing, which reads as "no stranded outputs" rather
	// than as a bug.
	want := lock.Bytes()

	// Read the wallet's own database directly. This is a recovery tool for a
	// state the wallet API has no vocabulary for -- "outputs you recorded but
	// never minted" -- so there is nothing to ask it. Read-only, and only ever
	// run by an operator.
	db, err := c.openWalletDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT t.txid, o.vout, o.satoshis, o.description
		FROM outputs o JOIN transactions t ON t.transaction_id = o.transaction_id
		WHERE o.spent_by IS NULL AND o.change = 0 AND o.locking_script = ?`, want)
	if err != nil {
		return nil, fmt.Errorf("chain: find stranded outputs: %w", err)
	}
	defer rows.Close()

	var out []StrandedOutput
	for rows.Next() {
		var s StrandedOutput
		var raw any
		if err := rows.Scan(&raw, &s.Vout, &s.Satoshis, &s.Description); err != nil {
			return nil, fmt.Errorf("chain: scan stranded output: %w", err)
		}
		s.TxID = txidString(raw)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Reclaim spends stranded outputs back into the wallet as change.
//
// They pay the wallet's own key, so the unlocking script is an ordinary P2PKH
// signature -- but the wallet will not produce it, because it has no record of
// these coins in its pool. So they go in as caller-provided inputs and we sign
// them ourselves, exactly as a tag output is spent, and declare no outputs so
// the whole value lands as change.
func (c *Chain) Reclaim(ctx context.Context, outs []StrandedOutput) (string, uint64, error) {
	if len(outs) == 0 {
		return "", 0, nil
	}
	walletKey, err := c.Identity.WalletKey()
	if err != nil {
		return "", 0, err
	}

	beef := transaction.NewBeefV2()
	inputs := make([]sdk.CreateActionInput, 0, len(outs))
	var total uint64

	for _, o := range outs {
		raws, rerr := c.Storage.RawTxs(ctx, []string{o.TxID})
		if rerr != nil {
			return "", 0, fmt.Errorf("chain: read %s: %w", o.TxID, rerr)
		}
		raw, ok := raws[o.TxID]
		if !ok || len(raw) == 0 {
			continue // not in the store; nothing we can do with it here
		}
		if _, merr := beef.MergeRawTx(raw, nil); merr != nil {
			return "", 0, fmt.Errorf("chain: wrap %s: %w", o.TxID, merr)
		}
		txid, perr := chainhash.NewHashFromHex(o.TxID)
		if perr != nil {
			return "", 0, fmt.Errorf("chain: parse %s: %w", o.TxID, perr)
		}
		inputs = append(inputs, sdk.CreateActionInput{
			Outpoint:              transaction.Outpoint{Txid: *txid, Index: o.Vout},
			InputDescription:      "stranded output",
			UnlockingScriptLength: 108, // P2PKH: 1 + 72 sig + 1 + 33 pubkey, rounded up
		})
		total += o.Satoshis
	}
	if len(inputs) == 0 {
		return "", 0, nil
	}

	inputBEEF, err := beef.Bytes()
	if err != nil {
		return "", 0, fmt.Errorf("chain: encode input beef: %w", err)
	}

	no := false
	created, err := c.Wallet.CreateAction(ctx, sdk.CreateActionArgs{
		Description: "reclaim stranded outputs",
		InputBEEF:   inputBEEF,
		Inputs:      inputs,
		Labels:      []string{AppLabel, "reclaim"},
		Options: &sdk.CreateActionOptions{
			SignAndProcess:         &no,
			RandomizeOutputs:       &no,
			AcceptDelayedBroadcast: &no,
		},
	}, c.cfg.Originator)
	if err != nil {
		return "", 0, fmt.Errorf("%w: build reclaim: %w", ErrNotBroadcast, err)
	}
	if created.SignableTransaction == nil {
		return "", 0, fmt.Errorf("%w: wallet returned no signable transaction", ErrNotBroadcast)
	}

	tx, err := transaction.NewTransactionFromBEEF(created.SignableTransaction.Tx)
	if err != nil {
		return "", 0, fmt.Errorf("%w: parse signable reclaim: %w", ErrNotBroadcast, err)
	}

	unlocker, err := p2pkh.Unlock(walletKey, nil)
	if err != nil {
		return "", 0, fmt.Errorf("%w: build unlocker: %w", ErrNotBroadcast, err)
	}
	spends := map[uint32]sdk.SignActionSpend{}
	for i := range inputs {
		sig, serr := unlocker.Sign(tx, uint32(i)) //nolint:gosec // bounded by len(inputs)
		if serr != nil {
			return "", 0, fmt.Errorf("%w: sign input %d: %w", ErrNotBroadcast, i, serr)
		}
		spends[uint32(i)] = sdk.SignActionSpend{UnlockingScript: sig.Bytes()} //nolint:gosec // as above
	}

	signed, err := c.Wallet.SignAction(ctx, sdk.SignActionArgs{
		Reference: created.SignableTransaction.Reference,
		Spends:    spends,
	}, c.cfg.Originator)
	if err != nil {
		return "", 0, fmt.Errorf("chain: broadcast reclaim: %w", err)
	}
	return signed.Txid.String(), total, nil
}

// txidString normalises the driver's representation of a txid column, which is
// raw bytes on SQLite and hex on Postgres.
func txidString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		// Already in display order. Reversing here -- the usual reflex with
		// Bitcoin txids -- produces ids that look plausible and match nothing,
		// so the lookup returns no rows and the caller reads it as "nothing to
		// do" rather than as a bug.
		return fmt.Sprintf("%x", t)
	}
	return ""
}

// openWalletDB opens the wallet's store read-only for the recovery path.
func (c *Chain) openWalletDB() (*sql.DB, error) {
	driver, dsn := "sqlite", filepath.Join(c.cfg.DataDir, "wallet.db")
	if c.cfg.PostgresDSN != "" {
		driver, dsn = "pgx", c.cfg.PostgresDSN
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("chain: open wallet store: %w", err)
	}
	return db, nil
}
