// Package tagscript builds, spends and decodes the locking script that holds a
// crab tag's reward on chain.
//
// The script is BRC-48 PushDrop shaped -- a key check followed by pushed data
// fields that are immediately dropped -- but with a two-of-two head rather than
// PushDrop's single OP_CHECKSIG:
//
//	<tagPub> OP_CHECKSIGVERIFY <dnrPub> OP_CHECKSIG <f0> <f1> ... <fn> OP_2DROP ...
//
// unlocked by
//
//	<dnrSig> <tagSig>
//
// The second key is not decoration. A tag's bearer secret is printed on a
// physical tag that never changes, so the crabber who reports a recapture holds
// exactly the same secret the next finder will hold. Without a second factor
// that first finder could come back and drain the escrow output holding their
// own bonus plus the next finder's reward. No key-derivation scheme fixes that:
// a past bearer can always re-derive whatever a future bearer derives. See the
// package README section on the escrow design.
//
// The cost is that redemption is DNR-gated -- a finder cannot unilaterally take
// the money. What the tag signature still proves on its own is physical
// possession of the tag, which is what binds a GPS attestation to a real
// animal, and a DNR refusal is publicly auditable because the output simply
// stays unspent.
//
// Because chunk 1 is OP_CHECKSIGVERIFY rather than OP_CHECKSIG, the go-sdk's
// pushdrop.Decode returns nil on these scripts. Decode below is the reader.
package tagscript

import (
	"bytes"
	"errors"
	"fmt"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/script/interpreter"
	"github.com/bsv-blockchain/go-sdk/transaction"
	sighash "github.com/bsv-blockchain/go-sdk/transaction/sighash"
)

// SigHashFlag is the signature scope for both halves of the two-of-two. Both
// signatures commit to every output, which is what binds the payout and the
// recapture record together: neither can be altered without invalidating both
// signatures.
const SigHashFlag = sighash.AllForkID

// UnlockingScriptEstimate is what to declare as CreateActionInput.UnlockingScriptLength.
//
// Two signatures, each a push of at most 72 bytes of DER plus one sighash byte,
// each preceded by a single push-opcode byte: 2 * (1 + 73) = 148. Rounded up.
//
// Over-declaring costs a few wasted fee satoshis. Under-declaring produces an
// underpaid transaction, which arcade rejects with a 4xx that is permanent and
// cannot be retried, so this number errs high on purpose.
const UnlockingScriptEstimate uint32 = 152

var (
	// ErrNotTagScript means the script is not shaped like a tag lock at all.
	ErrNotTagScript = errors.New("tagscript: not a tag locking script")
	// ErrEmptyField means a field was empty. Empty pushes are refused because
	// they are silently lossy on the way back out -- see Decode.
	ErrEmptyField = errors.New("tagscript: fields must not be empty")
	// ErrNoFields means a lock was requested with nothing to carry.
	ErrNoFields = errors.New("tagscript: at least one field is required")
)

// Lock builds the locking script for a tag output.
//
// Every field must be non-empty. That is not a style preference: a zero-length
// push encodes as OP_0, and a decoder walking the field list cannot distinguish
// "an empty field" from "the data ran out", so an empty field truncates every
// field after it. Encode refuses rather than writing a record that cannot be
// read back.
func Lock(tagPub, dnrPub *ec.PublicKey, fields [][]byte) (*script.Script, error) {
	if tagPub == nil || dnrPub == nil {
		return nil, errors.New("tagscript: both public keys are required")
	}
	if len(fields) == 0 {
		return nil, ErrNoFields
	}
	for i, f := range fields {
		if len(f) == 0 {
			return nil, fmt.Errorf("%w: field %d", ErrEmptyField, i)
		}
	}

	chunks := make([]*script.ScriptChunk, 0, len(fields)+5)
	chunks = append(chunks, pushChunk(tagPub.Compressed()))
	chunks = append(chunks, &script.ScriptChunk{Op: script.OpCHECKSIGVERIFY})
	chunks = append(chunks, pushChunk(dnrPub.Compressed()))
	chunks = append(chunks, &script.ScriptChunk{Op: script.OpCHECKSIG})

	for _, f := range fields {
		chunks = append(chunks, pushChunk(f))
	}
	// Drop the fields two at a time, then one if an odd field remains. This is
	// the same shape PushDrop emits, so the script reads as a familiar token to
	// anyone who has seen BRC-48.
	remaining := len(fields)
	for remaining > 1 {
		chunks = append(chunks, &script.ScriptChunk{Op: script.Op2DROP})
		remaining -= 2
	}
	if remaining == 1 {
		chunks = append(chunks, &script.ScriptChunk{Op: script.OpDROP})
	}

	s, err := script.NewScriptFromScriptOps(chunks)
	if err != nil {
		return nil, fmt.Errorf("tagscript: assemble locking script: %w", err)
	}
	return s, nil
}

// Unlock assembles the unlocking script from the two signatures.
//
// Order matters and is the reverse of the order the locking script checks them
// in. The unlocking script pushes dnrSig then tagSig, leaving tagSig on top;
// OP_CHECKSIGVERIFY runs first and consumes it, leaving dnrSig for the
// OP_CHECKSIG that follows.
//
// Both arguments must already carry the trailing sighash-type byte.
func Unlock(dnrSig, tagSig []byte) (*script.Script, error) {
	if len(dnrSig) == 0 || len(tagSig) == 0 {
		return nil, errors.New("tagscript: both signatures are required")
	}
	s := &script.Script{}
	if err := s.AppendPushData(dnrSig); err != nil {
		return nil, fmt.Errorf("tagscript: push dnr signature: %w", err)
	}
	if err := s.AppendPushData(tagSig); err != nil {
		return nil, fmt.Errorf("tagscript: push tag signature: %w", err)
	}
	return s, nil
}

// SigHash is the message both keys sign for the given input.
//
// The input must have its source output attached -- in the two-step
// CreateAction flow that comes free, because SignableTransaction.Tx is a BEEF
// carrying the parent.
func SigHash(tx *transaction.Transaction, inputIndex uint32) ([]byte, error) {
	if tx == nil || int(inputIndex) >= len(tx.Inputs) {
		return nil, errors.New("tagscript: input index out of range")
	}
	if tx.Inputs[inputIndex].SourceTxOutput() == nil {
		return nil, transaction.ErrEmptyPreviousTx
	}
	h, err := tx.CalcInputSignatureHash(inputIndex, SigHashFlag)
	if err != nil {
		return nil, fmt.Errorf("tagscript: signature hash: %w", err)
	}
	return h, nil
}

// SignWith produces one half of the two-of-two: a DER signature over the
// input's sighash with the sighash-type byte appended.
func SignWith(key *ec.PrivateKey, tx *transaction.Transaction, inputIndex uint32) ([]byte, error) {
	if key == nil {
		return nil, errors.New("tagscript: private key is required")
	}
	h, err := SigHash(tx, inputIndex)
	if err != nil {
		return nil, err
	}
	sig, err := key.Sign(h)
	if err != nil {
		return nil, fmt.Errorf("tagscript: sign: %w", err)
	}
	return append(sig.Serialize(), byte(SigHashFlag)), nil
}

// Decoded is what Decode recovers from a locking script.
type Decoded struct {
	TagPub *ec.PublicKey
	DNRPub *ec.PublicKey
	Fields [][]byte
}

// Decode reads a tag locking script back into its parts.
//
// It is a proven inverse, not a best-effort parse: after reading the pieces out
// it rebuilds the whole script from them and compares byte for byte. A script
// that parses but does not rebuild identically is reported as not-a-tag-script
// rather than returning fields that were never really in it. rule-110-arcade's
// cellscript.Decode uses the same discipline for the same reason -- a decoder
// that quietly disagrees with its encoder produces audit findings that point at
// the wrong thing.
func Decode(s *script.Script) (*Decoded, error) {
	if s == nil {
		return nil, ErrNotTagScript
	}
	chunks, err := s.Chunks()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotTagScript, err)
	}
	// Minimum shape: tagPub, CHECKSIGVERIFY, dnrPub, CHECKSIG, one field, DROP.
	if len(chunks) < 6 {
		return nil, ErrNotTagScript
	}
	if chunks[1].Op != script.OpCHECKSIGVERIFY || chunks[3].Op != script.OpCHECKSIG {
		return nil, ErrNotTagScript
	}
	tagPub, err := ec.PublicKeyFromBytes(chunks[0].Data)
	if err != nil {
		return nil, fmt.Errorf("%w: tag public key: %v", ErrNotTagScript, err)
	}
	dnrPub, err := ec.PublicKeyFromBytes(chunks[2].Data)
	if err != nil {
		return nil, fmt.Errorf("%w: dnr public key: %v", ErrNotTagScript, err)
	}

	var fields [][]byte
	for i := 4; i < len(chunks); i++ {
		op := chunks[i].Op
		if op == script.OpDROP || op == script.Op2DROP {
			break
		}
		if len(chunks[i].Data) == 0 {
			// Lock refuses to write these, so encountering one means the script
			// was not produced by this package.
			return nil, ErrNotTagScript
		}
		fields = append(fields, chunks[i].Data)
	}
	if len(fields) == 0 {
		return nil, ErrNotTagScript
	}

	rebuilt, err := Lock(tagPub, dnrPub, fields)
	if err != nil {
		return nil, fmt.Errorf("%w: rebuild: %v", ErrNotTagScript, err)
	}
	if !bytes.Equal(*rebuilt, *s) {
		return nil, fmt.Errorf("%w: script does not rebuild from its own parts", ErrNotTagScript)
	}

	return &Decoded{TagPub: tagPub, DNRPub: dnrPub, Fields: fields}, nil
}

// VerifyInput runs the real go-sdk interpreter over one input, the same check
// the toolbox's storage layer performs before it broadcasts. Running it
// ourselves first turns a permanent 4xx from arcade into a local error with a
// stack trace attached.
//
// No Chronicle opcodes are involved here, so the default (post-Genesis) rules
// are the right ones -- unlike rule-110-arcade, whose covenant emits OP_2MUL.
func VerifyInput(tx *transaction.Transaction, inputIndex int) error {
	if tx == nil || inputIndex < 0 || inputIndex >= len(tx.Inputs) {
		return errors.New("tagscript: input index out of range")
	}
	src := tx.Inputs[inputIndex].SourceTxOutput()
	if src == nil {
		return transaction.ErrEmptyPreviousTx
	}
	if err := interpreter.NewEngine().Execute(
		interpreter.WithTx(tx, inputIndex, src),
		interpreter.WithForkID(),
	); err != nil {
		return fmt.Errorf("tagscript: input %d rejected locally: %w", inputIndex, err)
	}
	return nil
}

// pushChunk builds a data push. Unlike PushDrop's minimal encoder we never
// collapse a small value to OP_1..OP_16 or OP_1NEGATE: those opcodes carry no
// Data, so a decoder has to reconstruct the pushed bytes from the opcode
// itself, and getting that wrong silently corrupts a field. Every field is a
// plain push, which makes Decode's byte-for-byte rebuild trivially exact.
func pushChunk(data []byte) *script.ScriptChunk {
	var op byte
	switch {
	case len(data) <= int(script.OpPUSHDATA1)-1:
		op = byte(len(data))
	case len(data) <= 0xff:
		op = script.OpPUSHDATA1
	case len(data) <= 0xffff:
		op = script.OpPUSHDATA2
	default:
		op = script.OpPUSHDATA4
	}
	return &script.ScriptChunk{Op: op, Data: data}
}
