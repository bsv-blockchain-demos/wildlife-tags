package chain

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/brc29"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"
)

// DepositAddress is where DNR sends money to fund the program.
//
// It is derived from fixed values in keys.json rather than a fresh derivation
// per deposit, so it survives restarts. A deposit address that moves is a
// silent failure: money sent to the old one is never seen, and the balance that
// does not include it is technically correct.
func (c *Chain) DepositAddress() (string, error) {
	walletKey, err := c.Identity.WalletKey()
	if err != nil {
		return "", err
	}

	// Anyone-can-pay: the sender is the well-known "anyone" key, so a funder
	// does not need to be told anything except the address.
	_, anyonePub := sdk.AnyoneKey()
	keyID := brc29.KeyID{
		DerivationPrefix: c.Identity.DerivationPrefix,
		DerivationSuffix: c.Identity.DerivationSuffix,
	}

	opt := brc29.WithTestNet()
	if c.cfg.Mainnet() {
		opt = brc29.WithMainNet()
	}
	addr, err := brc29.AddressForSelf(anyonePub, keyID, walletKey, opt)
	if err != nil {
		return "", fmt.Errorf("chain: derive deposit address: %w", err)
	}
	return addr.AddressString, nil
}

// ImportFunding takes a raw funding transaction and credits whatever it pays to
// the deposit address.
//
// It uses the wallet-payment protocol rather than basket insertion. That is not
// a stylistic choice: a basket-inserted coin records no BRC-29 derivation
// material, so the wallet can see it and cannot spend it -- which looks like a
// funded wallet right up until the first activation fails.
// merklePathHex, when non-empty, supplies the proof directly; otherwise it is
// fetched from arcade. A funding transaction must be mined before it can be
// imported, because InternalizeAction verifies the whole BEEF -- including the
// proof -- against locally held headers. That verification is the point: the
// wallet is not taking anyone's word that this money exists.
func (c *Chain) ImportFunding(ctx context.Context, rawTx []byte, merklePathHex string) (uint64, error) {
	// NewTransactionFromBytes reads extended format too, which is what a
	// Teranode-style funder hands out.
	tx, err := transaction.NewTransactionFromBytes(rawTx)
	if err != nil {
		return 0, fmt.Errorf("chain: parse funding transaction: %w", err)
	}
	txid := tx.TxID()

	if merklePathHex != "" {
		mp, mperr := transaction.NewMerklePathFromHex(merklePathHex)
		if mperr != nil {
			return 0, fmt.Errorf("chain: parse supplied merkle proof: %w", mperr)
		}
		tx.MerklePath = mp
	} else {
		res, mperr := c.Services.MerklePath(ctx, txid.String())
		if mperr != nil {
			return 0, fmt.Errorf("chain: fetch merkle proof for %s: %w", txid, mperr)
		}
		if res.MerklePath == nil {
			return 0, fmt.Errorf("chain: %s has no merkle proof yet; it must be mined before it can be imported", txid)
		}
		tx.MerklePath = res.MerklePath
	}

	addr, err := c.DepositAddress()
	if err != nil {
		return 0, err
	}

	prefix, err := base64.StdEncoding.DecodeString(c.Identity.DerivationPrefix)
	if err != nil {
		return 0, fmt.Errorf("chain: derivation prefix: %w", err)
	}
	suffix, err := base64.StdEncoding.DecodeString(c.Identity.DerivationSuffix)
	if err != nil {
		return 0, fmt.Errorf("chain: derivation suffix: %w", err)
	}
	_, anyonePub := sdk.AnyoneKey()

	var (
		outputs []sdk.InternalizeOutput
		total   uint64
	)
	for i, out := range tx.Outputs {
		if out.LockingScript == nil {
			continue
		}
		script := out.LockingScript.Bytes()
		matches, merr := c.outputPaysAddress(script, addr)
		if merr != nil {
			return 0, merr
		}
		if !matches {
			continue
		}
		outputs = append(outputs, sdk.InternalizeOutput{
			OutputIndex: uint32(i), //nolint:gosec // bounded by the output count
			Protocol:    sdk.InternalizeProtocolWalletPayment,
			PaymentRemittance: &sdk.Payment{
				DerivationPrefix:  prefix,
				DerivationSuffix:  suffix,
				SenderIdentityKey: anyonePub,
			},
		})
		total += out.Satoshis
	}
	if len(outputs) == 0 {
		return 0, fmt.Errorf("chain: no output in %s pays the deposit address %s", txid, addr)
	}

	beef, err := transaction.NewBeefFromTransaction(tx)
	if err != nil {
		return 0, fmt.Errorf("chain: build funding beef: %w", err)
	}
	atomic, err := beef.AtomicBytes(txid)
	if err != nil {
		return 0, fmt.Errorf("chain: encode funding beef: %w", err)
	}

	res, err := c.Wallet.InternalizeAction(ctx, sdk.InternalizeActionArgs{
		Tx:          atomic,
		Description: "crab tag program funding",
		Labels:      []string{AppLabel, "funding"},
		Outputs:     outputs,
	}, c.cfg.Originator)
	if err != nil {
		return 0, fmt.Errorf("chain: internalize funding: %w", err)
	}
	if !res.Accepted {
		return 0, fmt.Errorf("chain: wallet refused the funding transaction %s", txid)
	}
	return total, nil
}

// outputPaysAddress reports whether a locking script pays the given address.
func (c *Chain) outputPaysAddress(lockingScript []byte, addr string) (bool, error) {
	got, err := addressFromP2PKH(lockingScript, c.cfg.Mainnet())
	if err != nil {
		// Not a P2PKH output; funding transactions may legitimately carry
		// others, so this is not an error for the caller.
		return false, nil //nolint:nilerr // a non-P2PKH output simply is not a payment to us
	}
	return got == addr, nil
}

// WalletIdentityKey is the sender key a crabber's wallet needs in order to
// derive the payment it has been sent.
func (c *Chain) WalletIdentityKey() (*ec.PublicKey, error) {
	key, err := c.Identity.WalletKey()
	if err != nil {
		return nil, err
	}
	return key.PubKey(), nil
}

// WalletIdentityKeyHex is the same value in the form the API speaks.
func (c *Chain) WalletIdentityKeyHex() (string, error) {
	pub, err := c.WalletIdentityKey()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(pub.Compressed()), nil
}
