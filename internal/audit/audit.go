// Package audit reconciles what the chain says against what the database says.
//
// The programme's central claim is that its records are anchored on chain and
// cannot be altered afterwards. That claim is only worth something if somebody
// actually checks, and checking is exactly what this does: it walks every tag,
// pulls the real transactions out of the wallet store, decodes the locking
// scripts, re-verifies the attestations, and reports every place the two
// sources disagree.
//
// A finding is not necessarily a bug. A tag stuck in "redeeming" is usually a
// crabber who lost signal; a broadcast transaction with no proof yet is usually
// a block that has not arrived. The point is that nothing disagrees silently.
package audit

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/bsv-blockchain/go-sdk/transaction"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagscript"
)

// Severity ranks a finding.
type Severity string

const (
	// Critical means money or evidence is at stake: a locking script that does
	// not match what the database claims, or an attestation that does not
	// verify.
	Critical Severity = "critical"
	// Warning means something needs attention but nothing is provably wrong.
	Warning Severity = "warning"
	// Info means an observation worth printing.
	Info Severity = "info"
)

// Finding is one disagreement.
type Finding struct {
	Severity Severity `json:"severity"`
	TagID    string   `json:"tag_id,omitempty"`
	TxID     string   `json:"txid,omitempty"`
	Check    string   `json:"check"`
	Detail   string   `json:"detail"`
}

// Report is the result of a full reconciliation.
type Report struct {
	At          time.Time `json:"at"`
	TagsChecked int       `json:"tags_checked"`
	TxsChecked  int       `json:"transactions_checked"`
	Findings    []Finding `json:"findings"`
}

// Critical counts the findings that mean something is actually wrong.
func (r *Report) Criticals() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == Critical {
			n++
		}
	}
	return n
}

// RawTxSource supplies transactions by id. The wallet's storage provider
// satisfies it; declaring it here rather than importing the provider means the
// audit is testable without a wallet or a network.
type RawTxSource interface {
	RawTxs(ctx context.Context, txids []string) (map[string][]byte, error)
}

// Run reconciles the database against the chain.
func Run(ctx context.Context, s *store.Store, txs RawTxSource, coSignPubHex string) (*Report, error) {
	rep := &Report{At: time.Now().UTC()}

	tags, err := s.ListTags(ctx, "", 100000, 0)
	if err != nil {
		return nil, err
	}

	for _, tag := range tags {
		rep.TagsChecked++
		events, err := s.EventsForTag(ctx, tag.TagID)
		if err != nil {
			return nil, err
		}

		if err := auditTagState(rep, tag, events); err != nil {
			return nil, err
		}
		n, err := auditTagScripts(ctx, rep, txs, tag, events, coSignPubHex)
		if err != nil {
			return nil, err
		}
		rep.TxsChecked += n
	}

	if err := auditEscrows(ctx, rep, s); err != nil {
		return nil, err
	}
	return rep, nil
}

// auditTagState checks the database against itself: a tag's recorded state has
// to be consistent with the events that produced it.
func auditTagState(rep *Report, tag store.Tag, events []store.Event) error {
	var activations, recaptures int
	for _, ev := range events {
		if ev.Status == store.EventFailed {
			continue
		}
		switch ev.Kind {
		case string(record.KindActivate):
			activations++
		case string(record.KindRecapture):
			recaptures++
		}
	}

	switch tag.Status {
	case store.StatusMinted:
		if activations > 0 {
			add(rep, Critical, tag.TagID, "", "tag.state",
				fmt.Sprintf("tag is recorded as never armed but has %d activation records", activations))
		}
	case store.StatusActive, store.StatusCooldown:
		if activations == 0 {
			add(rep, Critical, tag.TagID, "", "tag.state",
				"tag holds a live reward but has no activation record")
		}
		if tag.LiveTxID == "" {
			add(rep, Critical, tag.TagID, "", "tag.state",
				"tag is live but names no output; the reward cannot be located")
		}
		if tag.LiveSatoshis == 0 {
			add(rep, Warning, tag.TagID, "", "tag.state", "tag is live but holds no satoshis")
		}
	case store.StatusRedeeming:
		// Usually a crabber who lost signal. The service releases these on a
		// timer, so one that is still here after a while is worth a look.
		add(rep, Warning, tag.TagID, "", "tag.state",
			"tag is mid-redemption; if this persists, its reward is unclaimable until it is released")
	}

	if uint32(recaptures) != tag.Generation && tag.Generation > 0 {
		add(rep, Warning, tag.TagID, "", "tag.generation",
			fmt.Sprintf("tag is at generation %d but has %d recapture records", tag.Generation, recaptures))
	}
	return nil
}

// auditTagScripts is the part that actually touches the chain.
//
// For every transaction the database claims recorded something, it fetches the
// real bytes, decodes the locking script, and checks three things: that the
// script is one of ours, that the record inside it says what the database says
// it says, and that its attestation still verifies. A row that passes all three
// is a row a researcher can rely on.
func auditTagScripts(ctx context.Context, rep *Report, txs RawTxSource, tag store.Tag, events []store.Event, coSignPubHex string) (int, error) {
	var txids []string
	for _, ev := range events {
		if ev.TxID != "" && ev.Status != store.EventFailed {
			txids = append(txids, ev.TxID)
		}
	}
	if len(txids) == 0 {
		return 0, nil
	}

	raws, err := txs.RawTxs(ctx, txids)
	if err != nil {
		// A missing wallet store is an audit that cannot run, not a finding
		// about the data.
		return 0, fmt.Errorf("audit: read transactions for %s: %w", tag.TagID, err)
	}

	checked := 0
	for _, ev := range events {
		if ev.TxID == "" || ev.Status == store.EventFailed {
			continue
		}
		raw, ok := raws[ev.TxID]
		if !ok || len(raw) == 0 {
			add(rep, Warning, tag.TagID, ev.TxID, "tx.missing",
				"the database names this transaction but the wallet store does not have it")
			continue
		}
		checked++

		tx, perr := parseTx(raw)
		if perr != nil {
			add(rep, Critical, tag.TagID, ev.TxID, "tx.parse", perr.Error())
			continue
		}
		if int(ev.Vout) >= len(tx.Outputs) {
			add(rep, Critical, tag.TagID, ev.TxID, "tx.vout",
				fmt.Sprintf("the database names output %d but the transaction has %d", ev.Vout, len(tx.Outputs)))
			continue
		}

		// Find the output carrying the record. For an activation that is the
		// one the database names. For a recapture it is not: the database names
		// the finder's *payment*, which is a plain P2PKH, while the record goes
		// into the re-locked output for the next generation. Looking only at
		// the named index is what made the first version of this audit skip
		// every recapture record silently.
		decoded, recordVout, found := findTagLock(tx, ev.Vout)
		if !found {
			if ev.Kind == string(record.KindRecapture) {
				// A recapture that retired the tag re-locks nothing, so there
				// is no record output to check. That is the normal end of a
				// tag's life, not a finding.
				continue
			}
			add(rep, Critical, tag.TagID, ev.TxID, "script.decode",
				"no output of this transaction is a tag lock")
			continue
		}
		_ = recordVout

		if got := hex.EncodeToString(decoded.TagPub.Compressed()); got != tag.PubKeyHex {
			add(rep, Critical, tag.TagID, ev.TxID, "script.tagkey",
				fmt.Sprintf("the output is locked to %s but this tag's key is %s", got, tag.PubKeyHex))
		}
		if got := hex.EncodeToString(decoded.DNRPub.Compressed()); got != coSignPubHex {
			add(rep, Critical, tag.TagID, ev.TxID, "script.cosignkey",
				fmt.Sprintf("the output names co-signing key %s, not this deployment's %s", got, coSignPubHex))
		}

		rec, rerr := record.Decode(decoded.Fields)
		if rerr != nil {
			add(rep, Critical, tag.TagID, ev.TxID, "record.decode", rerr.Error())
			continue
		}
		if rec.TagID != tag.TagID {
			add(rep, Critical, tag.TagID, ev.TxID, "record.tagid",
				fmt.Sprintf("the on-chain record names tag %s", rec.TagID))
		}
		if err := rec.Verify(); err != nil {
			// This is the one that matters most. A record whose attestation
			// does not verify names a tagger or a finder who did not sign it,
			// and it is permanent.
			add(rep, Critical, tag.TagID, ev.TxID, "record.attestation", err.Error())
		}
		if ev.PayloadJSON != "" && string(rec.Payload) != ev.PayloadJSON {
			add(rep, Critical, tag.TagID, ev.TxID, "record.payload",
				"the observation on chain differs from the one in the database")
		}
		if ev.SettlementJSON != "" && rec.Settled != nil && string(rec.Settled) != ev.SettlementJSON {
			add(rep, Critical, tag.TagID, ev.TxID, "record.settlement",
				"the settlement on chain differs from the one in the database")
		}
		auditSettlement(rep, tag, ev, tx, rec)

		if ev.Status == store.EventBroadcast {
			add(rep, Info, tag.TagID, ev.TxID, "tx.unproven",
				"broadcast but no verified merkle proof yet")
		}
	}
	return checked, nil
}

// findTagLock returns the first output of a transaction that decodes as a tag
// lock, preferring the index the caller names.
func findTagLock(tx *transaction.Transaction, preferred uint32) (*tagscript.Decoded, uint32, bool) {
	if int(preferred) < len(tx.Outputs) {
		if d, err := tagscript.Decode(tx.Outputs[preferred].LockingScript); err == nil {
			return d, preferred, true
		}
	}
	for i, out := range tx.Outputs {
		if d, err := tagscript.Decode(out.LockingScript); err == nil {
			return d, uint32(i), true //nolint:gosec // bounded by output count
		}
	}
	return nil, 0, false
}

// auditSettlement checks the unsigned half of a record against the transaction
// that carries it.
//
// The settlement is the only part of a record nobody signs, which is exactly
// why it is checked here instead. Every value in it is a claim about the
// transaction it sits in -- prev names the input, paid names output zero -- so
// the transaction itself is the authority, and a settlement that disagrees with
// its own transaction is a claim about money that did not move the way the
// record says.
func auditSettlement(rep *Report, tag store.Tag, ev store.Event, tx *transaction.Transaction, rec *record.Record) {
	if rec.Kind != record.KindRecapture {
		return
	}
	set, err := record.SettlementFromJSON(rec.Settled, rec.Payload, rec.Kind)
	if err != nil {
		add(rep, Critical, tag.TagID, ev.TxID, "settlement.decode", err.Error())
		return
	}

	// prev must be an input this transaction actually spends. The record is
	// written into the *next* generation's output, so the transaction carrying
	// it is the one that spent the output prev names.
	if set.Prev != "" {
		spent := false
		for _, in := range tx.Inputs {
			if in.SourceTXID != nil && in.SourceTXID.String() == set.Prev {
				spent = true
				break
			}
		}
		if !spent {
			add(rep, Critical, tag.TagID, ev.TxID, "settlement.prev",
				fmt.Sprintf("the record says it spends %s, but this transaction does not spend that output", set.Prev))
		}
	}

	// paid must be what output zero actually carries. That index is fixed by
	// construction and is where the finder's payment always sits.
	if set.PaidSat > 0 {
		if len(tx.Outputs) == 0 {
			add(rep, Critical, tag.TagID, ev.TxID, "settlement.paid",
				"the record claims a payment but the transaction has no outputs")
			return
		}
		if got := tx.Outputs[0].Satoshis; got != set.PaidSat {
			add(rep, Critical, tag.TagID, ev.TxID, "settlement.paid",
				fmt.Sprintf("the record says %d satoshis were paid, but output zero carries %d", set.PaidSat, got))
		}
	}
}

// auditEscrows checks that every promised re-release bonus is accounted for.
func auditEscrows(ctx context.Context, rep *Report, s *store.Store) error {
	unpaid, err := s.UnpaidEscrows(ctx, 100000)
	if err != nil {
		return err
	}
	for _, e := range unpaid {
		tag, terr := s.GetTag(ctx, e.TagID)
		if terr != nil {
			add(rep, Warning, e.TagID, "", "escrow.orphan",
				fmt.Sprintf("%d satoshis are owed to %s but the tag is unknown", e.Satoshis, short(e.Beneficiary)))
			continue
		}
		if tag.Status == store.StatusRetired {
			// A retired tag will never be reported again, so nothing can ever
			// corroborate the release this bonus was promised for. That is a
			// real obligation with no path to being met, and somebody has to
			// decide what to do about it.
			add(rep, Warning, e.TagID, "", "escrow.stranded",
				fmt.Sprintf("%d satoshis are owed to %s but the tag is retired, so no future recapture can release them",
					e.Satoshis, short(e.Beneficiary)))
		}
	}
	return nil
}

func add(rep *Report, sev Severity, tagID, txID, check, detail string) {
	rep.Findings = append(rep.Findings, Finding{
		Severity: sev, TagID: tagID, TxID: txID, Check: check, Detail: detail,
	})
}

func short(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12] + "…"
}
