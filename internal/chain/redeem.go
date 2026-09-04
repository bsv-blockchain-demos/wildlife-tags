package chain

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/brc29"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/wdk"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagscript"
)

// ErrNotBroadcast marks a failure that happened before SignAction was called.
//
// The boundary matters more than the error. Everything before SignAction is
// retractable: no transaction exists, the tag can go straight back into service
// and the pre-signing record can be deleted. Everything after it is not, because
// signing IS broadcasting -- the transaction may be on the network whatever this
// process does next -- so the record has to stand and the tag has to be treated
// as spent until the audit says otherwise.
var ErrNotBroadcast = errors.New("chain: not broadcast")

// PayoutSplit is who gets what out of a redemption.
type PayoutSplit struct {
	// ReporterSats goes to whoever is scanning the tag right now.
	ReporterSats uint64
	// EscrowSats goes to the *previous* reporter, released because this
	// recapture corroborates that they really did put the crab back.
	EscrowSats uint64
	// EscrowFor is that previous reporter's identity key. Empty on a tag's
	// first recapture, when there is nobody to corroborate.
	EscrowFor string
	// NextLockSats is re-locked for the next finder: this reporter's own
	// escrowed bonus plus a fresh base reward. Zero if the crab was kept.
	NextLockSats uint64
}

// RedeemRequest is a crabber's report, already signed by their wallet.
type RedeemRequest struct {
	TagID      tagkey.ID
	Ordinal    uint64
	Generation uint32 // the generation being spent

	// PrevTxID, PrevVout and PrevSatoshis locate the tag output being spent.
	PrevTxID     string
	PrevVout     uint32
	PrevSatoshis uint64

	// PayeePubHex is the finder's BRC-100 identity key, which the payment is
	// derived to and which their wallet internalises against.
	PayeePubHex string

	// Species names the profile this report is checked against. It must match
	// the species the tag was armed for; the caller enforces that, because only
	// the caller can read the tag's history.
	Species string

	Fix  species.Fix
	Meas map[string]int
	Attr map[string]string

	// Name is set only when this report is the one that names the animal.
	Name string

	// Observation is the canonical record the finder's wallet signed. The
	// server does not rebuild it: a signature over bytes assembled differently
	// is a signature over nothing. Settlement is the server's own half.
	Observation  []byte
	Settlement   []byte
	AttestSig    []byte
	AttestPubHex string

	// PendingEscrow, when set, is the bonus owed to the previous reporter.
	PendingEscrow *PayoutSplit
}

// Profile resolves the species profile this report is judged against.
func (r RedeemRequest) Profile() (*species.Profile, error) {
	code := r.Species
	if code == "" {
		code = species.Default
	}
	return species.Get(code)
}

// Disposition is what the finder did with the animal, read out of the signed
// observation's attributes.
func (r RedeemRequest) Disposition() species.Disposition {
	return species.Disposition(r.Attr[species.DispositionKey])
}

// RedemptionDraft is a redemption that has been built but not signed.
//
// It crosses the wire to the browser, which checks the payout before signing
// with the tag key. The server holds the matching draft until the signature
// comes back or the draft expires.
type RedemptionDraft struct {
	Reference []byte `json:"-"`

	TagID      string `json:"tag_id"`
	Generation uint32 `json:"generation"`

	// SignableTx is BEEF, so the browser can reconstruct the input's source
	// output and compute the same sighash the server will verify against.
	SignableTx []byte `json:"-"`
	InputIndex uint32 `json:"input_index"`

	// SourceSatoshis is the output being spent. The browser does not need the
	// source script separately: SignableTx is BEEF and carries the parent, so
	// the page reconstructs the same sighash the server verifies against.
	SourceSatoshis uint64 `json:"source_satoshis"`

	// The browser needs these three to ask its own wallet to derive the key the
	// payment output should be locked to, and then to internalise the payment
	// once it is broadcast.
	DerivationPrefix  string `json:"derivation_prefix"`
	DerivationSuffix  string `json:"derivation_suffix"`
	SenderIdentityKey string `json:"sender_identity_key"`

	Split PayoutSplit `json:"split"`
	// Observation is the finder's signed half; Settlement is the server's.
	Observation []byte `json:"-"`
	Settlement  []byte `json:"-"`

	// Retire records whether completing this redemption ends the tag's life.
	Retire bool `json:"retire"`

	// NextVout is where the re-locked output landed, or -1 if there is none.
	NextVout int `json:"next_vout"`

	ExpiresAt time.Time `json:"expires_at"`

	// Completion-path state the browser never sees. The escrow derivation in
	// particular has to survive: the previous reporter is not here when their
	// bonus is paid, so without a stored derivation their wallet has no way to
	// find the output later. See RedeemResult.EscrowKeyID.
	escrowVout   int
	escrowPrefix string
	escrowSuffix string
	// parentBEEF is the input BEEF this draft was built from. It is kept
	// because the payment handed to the crabber's wallet must carry its own
	// ancestry, and SignAction's result does not.
	parentBEEF []byte
}

// RedeemResult is a completed, broadcast redemption.
type RedeemResult struct {
	TxID string
	// AtomicBEEF is what the crabber's wallet internalises to see the money.
	AtomicBEEF []byte
	// PayoutVout is fixed at zero by construction; it is returned explicitly
	// because internalizeAction needs the index.
	PayoutVout uint32
	EscrowVout int
	NextVout   int
	Split      PayoutSplit

	// EscrowPrefix and EscrowSuffix let the previous reporter's wallet derive
	// the bonus output when they eventually come back for it. They are not
	// secret -- they travel in every BRC-29 remittance -- but they are the only
	// way that payment is ever found, so they must be persisted.
	EscrowPrefix string
	EscrowSuffix string
}

// payoutIndex is where the reporter's payment always sits.
//
// It is a constant rather than something the server reports, because the
// browser checks that index specifically before signing. If the server could
// name the index it was checked at, it could name one that happens to pay
// somebody else.
const payoutIndex uint32 = 0

// PrepareRedemption builds the unsigned redemption transaction.
//
// Nothing here is retractable-with-consequences: no signature exists yet, so
// every failure path is safe to unwind. The caller is responsible for having
// claimed the tag first.
func (c *Chain) PrepareRedemption(ctx context.Context, req RedeemRequest) (*RedemptionDraft, error) {
	profile, err := req.Profile()
	if err != nil {
		return nil, err
	}
	if err := profile.ValidateReport(req.Meas, req.Attr); err != nil {
		return nil, err
	}
	if err := req.Fix.Validate(); err != nil {
		return nil, err
	}
	payeePub, err := parsePubHex(req.PayeePubHex)
	if err != nil {
		return nil, fmt.Errorf("chain: finder identity key: %w", err)
	}

	split, err := c.splitFor(req)
	if err != nil {
		return nil, err
	}

	walletKey, err := c.Identity.WalletKey()
	if err != nil {
		return nil, err
	}
	senderIdentity, err := c.Identity.WalletIdentityKeyHex()
	if err != nil {
		return nil, err
	}

	// A fresh derivation per payment, so two rewards to the same crabber do not
	// share an address and the dataset does not leak their payment history to
	// anyone reading the chain.
	keyID, err := freshKeyID()
	if err != nil {
		return nil, err
	}

	netOpt := brc29.WithTestNet()
	if c.cfg.Mainnet() {
		netOpt = brc29.WithMainNet()
	}

	payoutLock, err := brc29.LockForCounterparty(walletKey, keyID, payeePub, netOpt)
	if err != nil {
		return nil, fmt.Errorf("chain: build payout lock: %w", err)
	}

	outputs := []sdk.CreateActionOutput{{
		LockingScript:     payoutLock.Bytes(),
		Satoshis:          split.ReporterSats,
		OutputDescription: "crab tag reward",
		Tags:              []string{AppLabel, "payout", string(req.TagID)},
	}}

	// The escrowed bonus, released to the previous reporter because the tag
	// turning up again is what corroborates their claim to have released it.
	escrowVout := -1
	var escrowKeyID brc29.KeyID
	if split.EscrowSats > 0 && split.EscrowFor != "" {
		beneficiary, perr := parsePubHex(split.EscrowFor)
		if perr != nil {
			return nil, fmt.Errorf("chain: escrow beneficiary key: %w", perr)
		}
		escrowKeyID, err = freshKeyID()
		if err != nil {
			return nil, err
		}
		escrowLock, lerr := brc29.LockForCounterparty(walletKey, escrowKeyID, beneficiary, netOpt)
		if lerr != nil {
			return nil, fmt.Errorf("chain: build escrow lock: %w", lerr)
		}
		escrowVout = len(outputs)
		outputs = append(outputs, sdk.CreateActionOutput{
			LockingScript:     escrowLock.Bytes(),
			Satoshis:          split.EscrowSats,
			OutputDescription: "tag re-release bonus",
			Tags:              []string{AppLabel, "escrow", string(req.TagID)},
		})
	}

	// The next generation's lock, carrying this recapture's record and the
	// money for whoever finds the crab next.
	nextVout := -1
	if split.NextLockSats > 0 {
		fields, ferr := c.buildRecaptureRecord(req, split)
		if ferr != nil {
			return nil, ferr
		}
		tagPub, kerr := c.tagPubKey(req.Ordinal)
		if kerr != nil {
			return nil, kerr
		}
		nextLock, lerr := tagscript.Lock(tagPub, c.CoSignPubKey(), fields)
		if lerr != nil {
			return nil, fmt.Errorf("chain: build next tag lock: %w", lerr)
		}
		instructions, merr := json.Marshal(tagInstructions{
			TagID: string(req.TagID), Ordinal: req.Ordinal, Generation: req.Generation + 1,
		})
		if merr != nil {
			return nil, fmt.Errorf("chain: encode custom instructions: %w", merr)
		}
		nextVout = len(outputs)
		outputs = append(outputs, sdk.CreateActionOutput{
			LockingScript:      nextLock.Bytes(),
			Satoshis:           split.NextLockSats,
			OutputDescription:  "tag reward",
			Basket:             TagBasket,
			CustomInstructions: string(instructions),
			Tags:               []string{AppLabel, "rec", string(req.TagID)},
		})
	}

	inputBEEF, err := c.inputBEEFFor(ctx, req.PrevTxID)
	if err != nil {
		return nil, err
	}

	prevTxID, err := chainhash.NewHashFromHex(req.PrevTxID)
	if err != nil {
		return nil, fmt.Errorf("chain: parse previous txid: %w", err)
	}

	no := false
	res, err := c.Wallet.CreateAction(ctx, sdk.CreateActionArgs{
		Description: fmt.Sprintf("redeem tag %s generation %d", req.TagID, req.Generation),
		InputBEEF:   inputBEEF,
		Inputs: []sdk.CreateActionInput{{
			Outpoint:         transaction.Outpoint{Txid: *prevTxID, Index: req.PrevVout},
			InputDescription: "tag reward",
			// Fee sizing only, and generous on purpose: an under-declared
			// length underpays the fee, and arcade rejects that with a
			// permanent 4xx that cannot be retried.
			UnlockingScriptLength: tagscript.UnlockingScriptEstimate,
		}},
		Outputs: outputs,
		Labels:  []string{AppLabel, "tag:" + string(req.TagID)},
		Options: &sdk.CreateActionOptions{
			// Leaving the input's unlocking script unset is what selects the
			// two-step path: CreateAction returns a signable transaction rather
			// than broadcasting one.
			SignAndProcess: &no,
			// Both signatures commit to every output, so the outputs must be
			// final before either is made. Randomising them would also move the
			// payout away from index zero, which is where the browser checks it.
			RandomizeOutputs:       &no,
			AcceptDelayedBroadcast: &no,
		},
	}, c.cfg.Originator)
	if err != nil {
		return nil, fmt.Errorf("%w: build redemption for %s: %w", ErrNotBroadcast, req.TagID, err)
	}
	if res.SignableTransaction == nil {
		return nil, fmt.Errorf("%w: wallet returned no signable transaction", ErrNotBroadcast)
	}

	draft := &RedemptionDraft{
		Reference:         res.SignableTransaction.Reference,
		TagID:             string(req.TagID),
		Generation:        req.Generation,
		SignableTx:        res.SignableTransaction.Tx,
		InputIndex:        0,
		SourceSatoshis:    req.PrevSatoshis,
		DerivationPrefix:  keyID.DerivationPrefix,
		DerivationSuffix:  keyID.DerivationSuffix,
		SenderIdentityKey: senderIdentity,
		Split:             split,
		Observation:       req.Observation,
		Settlement:        req.Settlement,
		Retire:            split.NextLockSats == 0,
		NextVout:          nextVout,
		// Short-lived on purpose: it pins a tag out of service and reserves
		// wallet inputs, and a crabber who has walked away should not hold
		// either.
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}
	draft.parentBEEF = inputBEEF
	draft.escrowVout = escrowVout
	draft.escrowPrefix = escrowKeyID.DerivationPrefix
	draft.escrowSuffix = escrowKeyID.DerivationSuffix
	return draft, nil
}

// CompleteRedemption adds DNR's co-signature and broadcasts.
//
// Past the SignAction call inside this function the transaction may exist on
// the network no matter what happens here, which is why the caller must have
// written its record first.
func (c *Chain) CompleteRedemption(ctx context.Context, draft *RedemptionDraft, tagSig []byte) (*RedeemResult, error) {
	if draft == nil {
		return nil, fmt.Errorf("%w: no draft", ErrNotBroadcast)
	}
	if time.Now().UTC().After(draft.ExpiresAt) {
		return nil, fmt.Errorf("%w: draft expired", ErrNotBroadcast)
	}
	if len(tagSig) == 0 {
		return nil, fmt.Errorf("%w: no tag signature", ErrNotBroadcast)
	}

	tx, err := transaction.NewTransactionFromBEEF(draft.SignableTx)
	if err != nil {
		return nil, fmt.Errorf("%w: parse signable transaction: %w", ErrNotBroadcast, err)
	}

	dnrSig, err := tagscript.SignWith(c.coSignKey, tx, draft.InputIndex)
	if err != nil {
		return nil, fmt.Errorf("%w: co-sign: %w", ErrNotBroadcast, err)
	}
	unlock, err := tagscript.Unlock(dnrSig, tagSig)
	if err != nil {
		return nil, fmt.Errorf("%w: assemble unlocking script: %w", ErrNotBroadcast, err)
	}
	tx.Inputs[draft.InputIndex].UnlockingScript = unlock

	// Run the interpreter before broadcasting. A crabber whose wallet produced
	// a bad signature should see a clear refusal, not a permanent 4xx from
	// arcade several seconds later -- and a tag that is spent-but-invalid is a
	// far worse state to recover from.
	if err := tagscript.VerifyInput(tx, int(draft.InputIndex)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotBroadcast, err)
	}

	// Everything past this line may exist on the network.
	signed, err := c.Wallet.SignAction(ctx, sdk.SignActionArgs{
		Reference: draft.Reference,
		Spends:    map[uint32]sdk.SignActionSpend{draft.InputIndex: {UnlockingScript: unlock.Bytes()}},
	}, c.cfg.Originator)
	if err != nil {
		return nil, fmt.Errorf("chain: broadcast redemption for %s: %w", draft.TagID, err)
	}

	atomic, err := c.paymentBEEF(ctx, signed, draft.parentBEEF)
	if err != nil {
		// The transaction is already on the network at this point. Failing here
		// would tell the crabber their payment failed when it did not, so the
		// redemption stands and the receipt simply carries no BEEF: their
		// wallet can still be pointed at the txid.
		c.logger.Error("broadcast a redemption but could not build the payment BEEF",
			"tag", draft.TagID, "txid", signed.Txid.String(), "error", err)
		atomic = nil
	}

	return &RedeemResult{
		TxID:         signed.Txid.String(),
		AtomicBEEF:   atomic,
		PayoutVout:   payoutIndex,
		EscrowVout:   draft.escrowVout,
		NextVout:     draft.NextVout,
		Split:        draft.Split,
		EscrowPrefix: draft.escrowPrefix,
		EscrowSuffix: draft.escrowSuffix,
	}, nil
}

// splitFor works out who gets what.
//
// The shape is the whole incentive design. A first recapture pays the reporter
// their base and, if they put the crab back, escrows their bonus into the next
// generation's output rather than paying it now -- because nothing on chain can
// prove a release happened, and the only thing that can corroborate it is the
// tag turning up again. Every later recapture pays the current reporter's base
// and releases the previous reporter's escrowed bonus at the same time.
func (c *Chain) splitFor(req RedeemRequest) (PayoutSplit, error) {
	split := PayoutSplit{ReporterSats: c.cfg.BaseSatoshis}

	if req.PendingEscrow != nil && req.PendingEscrow.EscrowSats > 0 {
		split.EscrowSats = req.PendingEscrow.EscrowSats
		split.EscrowFor = req.PendingEscrow.EscrowFor
	}

	if req.Disposition() == species.Released {
		// This reporter's own bonus, plus a fresh reward for whoever finds the
		// crab next, locked into one output.
		split.NextLockSats = c.cfg.BonusSatoshis + c.cfg.BaseSatoshis
	}

	// A tag output must always cover the reporter's base; anything beyond that
	// is topped up from the wallet.
	if req.PrevSatoshis < split.ReporterSats+split.EscrowSats {
		return PayoutSplit{}, fmt.Errorf(
			"chain: tag output holds %d satoshis, less than the %d owed to the reporter and the previous finder",
			req.PrevSatoshis, split.ReporterSats+split.EscrowSats)
	}
	return split, nil
}

// buildRecaptureRecord assembles the fields written into the next generation's
// locking script, and verifies the crabber's attestation before they go on chain.
func (c *Chain) buildRecaptureRecord(req RedeemRequest, split PayoutSplit) ([][]byte, error) {
	_ = split
	pub, err := parsePubHex(req.AttestPubHex)
	if err != nil {
		return nil, fmt.Errorf("chain: finder attestation key: %w", err)
	}
	fields, err := record.Encode(string(req.TagID), record.KindRecapture, req.Generation+1,
		req.Observation, req.AttestSig, pub, req.Settlement)
	if err != nil {
		return nil, fmt.Errorf("chain: encode recapture: %w", err)
	}
	decoded, err := record.Decode(fields)
	if err != nil {
		return nil, fmt.Errorf("chain: re-read recapture: %w", err)
	}
	if err := decoded.Verify(); err != nil {
		return nil, fmt.Errorf("chain: recapture attestation: %w", err)
	}
	return fields, nil
}

// inputBEEFFor wraps the parent transaction minimally.
//
// Deliberately not the parent's whole ancestry. A tag's outputs form a chain
// that lengthens with every recapture, and passing accumulated ancestry here
// grows the BEEF without bound -- the sibling applications measured over a
// megabyte of ancestry attached to a four-kilobyte transaction before pinning
// this down.
func (c *Chain) inputBEEFFor(ctx context.Context, txid string) ([]byte, error) {
	raws, err := c.Storage.RawTxs(ctx, []string{txid})
	if err != nil {
		return nil, fmt.Errorf("chain: read parent transaction %s: %w", txid, err)
	}
	raw, ok := raws[txid]
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("chain: parent transaction %s is not in the wallet store", txid)
	}

	beef := transaction.NewBeefV2()
	if _, err := beef.MergeRawTx(raw, nil); err != nil {
		return nil, fmt.Errorf("chain: wrap parent transaction: %w", err)
	}
	b, err := beef.Bytes()
	if err != nil {
		return nil, fmt.Errorf("chain: encode input beef: %w", err)
	}
	return b, nil
}

// paymentBEEF renders a broadcast transaction in the form a BRC-100 wallet
// internalizes.
//
// Two things have to be right, and each was got wrong once.
//
// The parent has to be merged in explicitly. SignAction hands back the signed
// transaction without its ancestry, and a BEEF built from that alone is
// rejected as incomplete -- which shows up not at broadcast, where everything
// looks fine, but at the finder's wallet, which cannot credit a payment it
// cannot verify.
//
// And the parent has to carry its merkle proof. A receiving wallet verifies a
// BEEF by walking back to a proven transaction; inputBEEFFor deliberately wraps
// the parent with no proof, because the BEEF handed to *storage* only has to
// describe the input. The BEEF handed to a *finder* has a different job: it has
// to stand on its own. Without the proof the walk reaches nothing proven and
// the wallet refuses the payment with WERR_INVALID_PARAMETER('tx', 'valid
// AtomicBEEF') -- a message about the argument, for a problem that is nothing
// of the kind.
//
// A parent that is not yet mined has no proof to attach, and then the BEEF
// genuinely cannot be verified yet. That is not an error here: the money has
// moved, and the receipt still names the transaction. The client keeps it and
// internalizes once a block arrives.
func (c *Chain) paymentBEEF(ctx context.Context, signed *sdk.SignActionResult, parentBEEF []byte) ([]byte, error) {
	beef := transaction.NewBeefV2()

	// Whatever the parent chain was, it came in through the draft.
	if len(parentBEEF) > 0 {
		if parent, err := transaction.NewBeefFromBytes(parentBEEF); err == nil {
			for _, btx := range parent.Transactions {
				if btx.Transaction == nil {
					continue
				}
				if _, merr := beef.MergeTransaction(btx.Transaction); merr != nil {
					return nil, fmt.Errorf("chain: merge parent into payment beef: %w", merr)
				}
			}
		}
	}

	tx, err := transaction.NewTransactionFromBEEF(signed.Tx)
	if err != nil {
		// SignAction may return raw bytes rather than BEEF depending on
		// options; fall back rather than failing a payment that has already
		// been broadcast.
		tx, err = transaction.NewTransactionFromBytes(signed.Tx)
		if err != nil {
			return nil, fmt.Errorf("chain: parse broadcast transaction: %w", err)
		}
	}
	if _, err := beef.MergeTransaction(tx); err != nil {
		return nil, fmt.Errorf("chain: merge payment into beef: %w", err)
	}

	// Now give every unproven transaction in the BEEF its proof, if the chain
	// has one.
	//
	// Every one, not just the tag's parent. A redemption spends two inputs: the
	// tag output, and a coin from the wallet to top the payment up. Both source
	// transactions end up in the BEEF, and the receiving wallet's walk stops at
	// whichever of them it cannot verify -- so proving one and not the other
	// achieves nothing. The first version of this did exactly that, and the
	// BEEF stayed unverifiable for a reason that was invisible from the code.
	c.attachProofs(ctx, beef)

	txid := tx.TxID()
	atomic, err := beef.AtomicBytes(txid)
	if err != nil {
		return nil, fmt.Errorf("chain: encode atomic beef: %w", err)
	}
	return atomic, nil
}

// attachProofs fills in every merkle path the chain can supply.
//
// Deliberately after assembly rather than during it: what needs a proof is
// whatever ended up in the BEEF, and merging a transaction pulls its own input
// sources in behind it. Deciding one at a time on the way in misses those.
func (c *Chain) attachProofs(ctx context.Context, beef *transaction.Beef) {
	src := c.proofs()
	if src == nil {
		return
	}
	for _, btx := range beef.Transactions {
		if btx.Transaction == nil || btx.Transaction.MerklePath != nil {
			continue
		}
		attachProof(ctx, c.logger, src, btx.Transaction)
		// Merging it back is what records the bump against this BEEF; setting
		// the field on the transaction alone does not put it in the encoding.
		if btx.Transaction.MerklePath != nil {
			if _, err := beef.MergeTransaction(btx.Transaction); err != nil {
				c.logger.Warn("could not record a proof in the payment beef",
					"txid", btx.Transaction.TxID().String(), "error", err)
			}
		}
	}
}

// ProofSource looks up a transaction's merkle path.
//
// Declared here rather than taking *services.Services, so the BEEF assembly can
// be tested without a network. That is not ceremony: attaching the proof is the
// difference between a finder's wallet crediting a payment and refusing it, and
// the first version of this code silently did not attach it at all.
type ProofSource interface {
	MerklePath(ctx context.Context, txid string) (*wdk.MerklePathResult, error)
}

// proofs returns whatever this deployment can look proofs up with.
func (c *Chain) proofs() ProofSource {
	if c.Services == nil {
		return nil
	}
	return c.Services
}

// attachProof gives a transaction its merkle path, if the chain has one yet.
//
// Best effort by design. A transaction broadcast a moment ago has no proof, and
// asking for one is not an error -- it is the normal state for the first few
// minutes. Anything that goes wrong here costs the receipt its verifiability,
// not the payment its validity, so it is logged and stepped over rather than
// failing a redemption that has already moved money.
//
// Logged at info, not debug. Whether a payment went out verifiable is exactly
// the kind of thing somebody reads the log to find out after a finder says
// their wallet would not take it.
func attachProof(ctx context.Context, logger *slog.Logger, src ProofSource, tx *transaction.Transaction) {
	if tx == nil || tx.MerklePath != nil || src == nil {
		return
	}
	txid := tx.TxID().String()
	res, err := src.MerklePath(ctx, txid)
	if err != nil {
		logger.Info("no merkle proof for a payment's parent yet", "txid", txid, "error", err)
		return
	}
	if res == nil || res.MerklePath == nil {
		logger.Info("a payment's parent is not mined yet, so its BEEF cannot be verified until it is",
			"txid", txid)
		return
	}
	tx.MerklePath = res.MerklePath
	logger.Info("attached a merkle proof to a payment's parent",
		"txid", txid, "height", res.MerklePath.BlockHeight)
}

// freshKeyID draws a new BRC-29 derivation pair.
func freshKeyID() (brc29.KeyID, error) {
	var prefix, suffix [8]byte
	if _, err := rand.Read(prefix[:]); err != nil {
		return brc29.KeyID{}, fmt.Errorf("chain: derivation prefix: %w", err)
	}
	if _, err := rand.Read(suffix[:]); err != nil {
		return brc29.KeyID{}, fmt.Errorf("chain: derivation suffix: %w", err)
	}
	return brc29.KeyID{
		DerivationPrefix: base64Std(prefix[:]),
		DerivationSuffix: base64Std(suffix[:]),
	}, nil
}

// AbortDraft releases the wallet inputs a half-built redemption reserved.
//
// CreateAction reserves real coins the moment it returns a signable
// transaction, and they stay reserved until that transaction is either signed
// or explicitly abandoned. Putting the tag back into service without doing this
// fixes only half the problem: the tag is claimable again but the wallet has
// less money than its balance suggests, and with a small pool the next
// redemption fails with "not enough funds" while the balance reads healthy.
//
// Safe to call on a reference the wallet has already forgotten; that is the
// normal case after a completed redemption.
func (c *Chain) AbortDraft(ctx context.Context, reference []byte) error {
	if len(reference) == 0 {
		return nil
	}
	if _, err := c.Wallet.AbortAction(ctx, sdk.AbortActionArgs{Reference: reference}, c.cfg.Originator); err != nil {
		return fmt.Errorf("chain: abort draft: %w", err)
	}
	return nil
}
