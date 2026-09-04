package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/arcade"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/chain"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// Rearm brings a cooled-down tag back into service.
//
// It is a deliberate act rather than a timer expiring on its own, because the
// cooldown is the one window in which DNR can look at a report before the tag
// becomes claimable again. Automating it away would remove the only human
// checkpoint in the loop.
func (s *Service) Rearm(ctx context.Context, tagID, actor string) error {
	tag, err := s.store.GetTag(ctx, tagID)
	if err != nil {
		return err
	}
	if tag.Status != store.StatusCooldown {
		return fmt.Errorf("service: tag %s is %s, not waiting to be re-armed", tagID, tag.Status)
	}
	if tag.CooldownUntil != nil && s.now().Before(*tag.CooldownUntil) {
		return fmt.Errorf("%w: it can be re-armed after %s", ErrCooldown, formatWhen(tag.CooldownUntil))
	}

	// The output re-locked by the previous redemption is already funded and
	// already carries the next reward; re-arming is a database transition, not
	// a transaction. Spending money here would double-fund the tag.
	if err := s.store.RearmTag(ctx, tagID, tag.Generation, tag.LiveTxID, tag.LiveVout, tag.LiveSatoshis); err != nil {
		return err
	}
	if err := s.store.Audit(ctx, actor, "tag.rearm", tagID); err != nil {
		s.logger.Warn("could not record a re-arm in the audit log", "tag", tagID, "error", err)
	}
	return nil
}

// PendingRearms lists tags whose cooldown has elapsed.
func (s *Service) PendingRearms(ctx context.Context, limit int) ([]store.Tag, error) {
	return s.store.CooledDownTags(ctx, s.now(), limit)
}

// SweepResult reports what a sweep reclaimed.
type SweepResult struct {
	TagID    string `json:"tag_id"`
	TxID     string `json:"txid"`
	Satoshis uint64 `json:"satoshis"`
	Err      string `json:"error,omitempty"`
}

// SweepExpired reclaims rewards from tags that were never reported.
//
// Blue crabs shed their carapace -- and anything wired to it -- at every moult,
// and they live two or three years. A meaningful fraction of any batch is on
// the seabed within months, and without this their rewards stay locked forever.
// A programme that permanently burns money for every crab that dies is one
// nobody funds twice.
func (s *Service) SweepExpired(ctx context.Context, limit int, actor string) ([]SweepResult, error) {
	tags, err := s.store.SweepableTags(ctx, s.now(), limit)
	if err != nil {
		return nil, err
	}
	return s.sweep(ctx, tags, actor)
}

// SweepTag reclaims one tag's reward immediately, regardless of its sweep date.
//
// The date rule exists so a live tag is not reclaimed out from under a crabber
// who is about to find it. This bypasses it deliberately, for the cases where
// waiting is wrong: a tag reported stolen, a batch recalled after a printing
// error, a programme cancelled, or a deployment being decommissioned. It is
// audit-logged like every other sweep, and the actor is recorded, because
// taking a live reward off the water is exactly the kind of act that should be
// attributable afterwards.
func (s *Service) SweepTag(ctx context.Context, tagID, actor string) (*SweepResult, error) {
	tag, err := s.store.GetTag(ctx, tagID)
	if err != nil {
		return nil, err
	}
	switch tag.Status {
	case store.StatusActive, store.StatusCooldown:
	default:
		return nil, fmt.Errorf("service: tag %s is %s and holds nothing to reclaim", tagID, tag.Status)
	}

	results, err := s.sweep(ctx, []store.Tag{*tag}, actor)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		// Another redemption held the tag; the sweep declined rather than race it.
		return nil, fmt.Errorf("%w: %s is being redeemed right now", ErrTagBusy, tagID)
	}
	return &results[0], nil
}

// sweep reclaims a set of tags. Shared by the scheduled and the targeted paths
// so both take the tag out of service the same way and log the same audit trail.
func (s *Service) sweep(ctx context.Context, tags []store.Tag, actor string) ([]SweepResult, error) {

	results := make([]SweepResult, 0, len(tags))
	for _, tag := range tags {
		res := SweepResult{TagID: tag.TagID, Satoshis: tag.LiveSatoshis}

		// Take the tag out of service first. A sweep and a redemption racing on
		// the same output would have one of them rejected by the chain, and the
		// one that loses should not be the crabber's.
		from := tag.Status
		if err := s.store.Transition(ctx, tag.TagID, from, store.StatusRedeeming); err != nil {
			if errors.Is(err, store.ErrTagNotAvailable) {
				continue // somebody is redeeming it right now; leave it alone
			}
			return results, err
		}

		txid, serr := s.chain.Sweep(ctx, chain.SweepRequest{
			TagID:        tagkey.ID(tag.TagID),
			Ordinal:      tag.Ordinal,
			PrevTxID:     tag.LiveTxID,
			PrevVout:     tag.LiveVout,
			PrevSatoshis: tag.LiveSatoshis,
		})
		if serr != nil {
			res.Err = serr.Error()
			if errors.Is(serr, chain.ErrNotBroadcast) {
				// Nothing happened on chain; put it back exactly as it was.
				if rerr := s.store.Transition(ctx, tag.TagID, store.StatusRedeeming, from); rerr != nil {
					s.logger.Error("could not restore a tag after a failed sweep", "tag", tag.TagID, "error", rerr)
				}
			}
			results = append(results, res)
			continue
		}

		res.TxID = txid
		if err := s.store.RetireTag(ctx, tag.TagID); err != nil {
			s.logger.Error("swept a tag but could not retire it", "tag", tag.TagID, "txid", txid, "error", err)
		}
		if err := s.store.Audit(ctx, actor, "tag.sweep", fmt.Sprintf("%s reclaimed %d satoshis at %s", tag.TagID, tag.LiveSatoshis, txid)); err != nil {
			s.logger.Warn("could not record a sweep in the audit log", "tag", tag.TagID, "error", err)
		}
		results = append(results, res)
	}
	return results, nil
}

// FollowChainStatus keeps the application's view of transaction status current.
//
// It hangs off the chain's single status observer rather than opening its own
// subscription: arcade's event stream has no per-client filter, so a second
// connection on the same callback token receives a full duplicate of every
// event and doubles arcade's fan-out cost for nothing.
//
// The observer contract is strict and worth restating where it is implemented:
// this runs inline on the applier goroutine, so it must not block and must not
// panic; delivery is at-least-once, so it must be idempotent; and the slice
// must not be retained.
func (s *Service) FollowChainStatus(ctx context.Context) {
	s.chain.OnStatus(func(records []arcade.TxRecord) {
		for _, rec := range records {
			switch rec.Status {
			case arcade.StatusMined, arcade.StatusImmutable:
				// "Mined" here is arcade's word. The merkle proof itself is
				// fetched and verified against locally held headers by the
				// monitor before the wallet promotes the coin; this only mirrors
				// the outcome into the application's own event log so the
				// public pages can show a proof status.
				if err := s.store.MarkEventMined(ctx, rec.TxID); err != nil {
					s.logger.Warn("could not mark an event mined", "txid", rec.TxID, "error", err)
				}
			case arcade.StatusRejected, arcade.StatusDoubleSpendAttempted:
				if err := s.store.FailEventByTxID(ctx, rec.TxID, string(rec.Status)+": "+rec.ExtraInfo); err != nil {
					s.logger.Warn("could not mark an event failed", "txid", rec.TxID, "error", err)
				}
			}
		}
	})
}
