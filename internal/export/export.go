// Package export writes the public dataset.
//
// The rows are the point of the whole programme: a movement study needs where
// and when each animal was tagged and where and when it turned up again, and it
// needs to be able to say where each number came from. Every row therefore
// carries the transaction that recorded it and whether that transaction has a
// verified merkle proof, so a researcher can check any single line against the
// chain rather than taking the file's word for it.
//
// # Why the columns are not fixed
//
// A blue crab has a carapace width and a moult stage; a red drum has a total
// length and neither. Hardcoding one species' fields as columns is what made
// the first version of this file blue-crab-shaped, and it would have meant a
// release for every new species. So the measurements and attributes are the
// species profile's, and the CSV's columns are whatever the data actually
// contains -- discovered in a first pass, written in the second. The JSON form
// keeps them as objects and needs no such pass.
package export

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
)

// Row is one tag event as a researcher sees it.
type Row struct {
	TagID      string  `json:"tag_id"`
	Event      string  `json:"event"`
	Generation uint32  `json:"generation"`
	At         string  `json:"at"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	AccuracyM  float64 `json:"accuracy_m"`

	Species string `json:"species,omitempty"`
	Name    string `json:"name,omitempty"`

	// Meas and Attr are the species profile's fields, exactly as they were
	// signed. Keys are the profile's short codes -- "cw", "tl", "sex" -- which
	// is what the schema endpoint documents.
	Meas map[string]int    `json:"meas,omitempty"`
	Attr map[string]string `json:"attr,omitempty"`

	Disposition string `json:"disposition,omitempty"`

	DaysAtLarge int `json:"days_at_large,omitempty"`
	DistanceM   int `json:"distance_m,omitempty"`

	// QueuedSec is how long the observation waited on a device before it
	// reached the server. A non-zero value means the fix was taken out of
	// signal range and submitted later, which is a fact about the row's
	// provenance rather than about the animal.
	QueuedSec int `json:"queued_seconds,omitempty"`

	// AttestedBy is the identity key that signed this record. Activations
	// signed by a named biologist are distinguishable here from ones entered at
	// an operator console, which is a distinction a reviewer will want.
	AttestedBy string `json:"attested_by,omitempty"`

	// TxID and Proven are how a reader checks a row rather than trusting it.
	// Proven means a merkle proof for that transaction has been verified
	// against locally held block headers -- not merely that it was accepted.
	TxID   string `json:"txid"`
	Proven bool   `json:"proven"`

	PaidSats uint64 `json:"paid_satoshis,omitempty"`
}

// fixedHeader is every column that does not depend on the species.
var fixedHeader = []string{
	"tag_id", "event", "generation", "at", "lat", "lon", "accuracy_m",
	"species", "name", "disposition", "days_at_large", "distance_m",
	"queued_seconds", "attested_by", "txid", "proven", "paid_satoshis",
}

// Rows streams the dataset out of the database.
func Rows(ctx context.Context, s *store.Store, fn func(Row) error) error {
	return s.AllEvents(ctx, func(ev store.Event) error {
		// A failed transaction is not a data point. It recorded nothing that
		// happened, and including it would put phantom animals in a movement
		// study.
		if ev.Status == store.EventFailed || ev.PayloadJSON == "" {
			return nil
		}
		kind := record.Kind(ev.Kind)
		if kind != record.KindActivate && kind != record.KindRecapture {
			return nil
		}

		obs, err := record.ObservationFromJSON([]byte(ev.PayloadJSON), kind)
		if err != nil {
			return fmt.Errorf("export: decode %s record for %s: %w", ev.Kind, ev.TagID, err)
		}
		set, err := record.SettlementFromJSON([]byte(ev.SettlementJSON), []byte(ev.PayloadJSON), kind)
		if err != nil {
			return fmt.Errorf("export: decode %s settlement for %s: %w", ev.Kind, ev.TagID, err)
		}

		row := Row{
			TagID:       ev.TagID,
			Event:       "tagged",
			Generation:  ev.Generation,
			At:          obs.TS,
			Lat:         record.DecodeCoord(obs.LatE7),
			Lon:         record.DecodeCoord(obs.LonE7),
			AccuracyM:   float64(obs.AccCM) / 100,
			Species:     obs.Sp,
			Name:        obs.Name,
			Meas:        obs.Meas,
			Attr:        obs.Attr,
			Disposition: string(obs.Disposition()),
			QueuedSec:   set.QueueSec,
			AttestedBy:  ev.AttestPubKey,
			TxID:        ev.TxID,
			Proven:      ev.Status == store.EventMined,
		}
		if kind == record.KindRecapture {
			row.Event = "recaptured"
			row.DaysAtLarge = set.DaysAt
			row.DistanceM = set.DistM
			row.PaidSats = set.PaidSat
		}
		return fn(row)
	})
}

// columns discovers which profile fields the dataset actually contains.
//
// A first full pass, so the header can name every column before the first row
// is written. The alternative -- a fixed union of every shipped profile's
// fields -- would put empty carapace-width columns in a red drum export forever,
// and would still be wrong the moment a profile is added.
func columns(ctx context.Context, s *store.Store) (meas, attr []string, err error) {
	seenMeas := map[string]bool{}
	seenAttr := map[string]bool{}
	err = Rows(ctx, s, func(r Row) error {
		for k := range r.Meas {
			seenMeas[k] = true
		}
		for k := range r.Attr {
			seenAttr[k] = true
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	for k := range seenMeas {
		meas = append(meas, k)
	}
	for k := range seenAttr {
		attr = append(attr, k)
	}
	sort.Strings(meas)
	sort.Strings(attr)
	return meas, attr, nil
}

// CSV writes the dataset as comma-separated values.
func CSV(ctx context.Context, s *store.Store, w io.Writer) error {
	measKeys, attrKeys, err := columns(ctx, s)
	if err != nil {
		return err
	}

	header := append([]string{}, fixedHeader...)
	for _, k := range measKeys {
		header = append(header, "meas_"+k)
	}
	for _, k := range attrKeys {
		header = append(header, "attr_"+k)
	}

	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return fmt.Errorf("export: write header: %w", err)
	}
	err = Rows(ctx, s, func(r Row) error {
		line := []string{
			r.TagID, r.Event, strconv.FormatUint(uint64(r.Generation), 10), r.At,
			f(r.Lat, 7), f(r.Lon, 7), f(r.AccuracyM, 2),
			r.Species, r.Name, r.Disposition, i(r.DaysAtLarge), i(r.DistanceM),
			i(r.QueuedSec), r.AttestedBy, r.TxID, b(r.Proven), strconv.FormatUint(r.PaidSats, 10),
		}
		for _, k := range measKeys {
			if v, ok := r.Meas[k]; ok {
				line = append(line, strconv.Itoa(v))
				continue
			}
			// Empty rather than zero: a measurement nobody took and a
			// measurement of zero are different facts, and a movement study
			// that cannot tell them apart is a movement study with invented
			// data in it.
			line = append(line, "")
		}
		for _, k := range attrKeys {
			line = append(line, r.Attr[k])
		}
		return cw.Write(line) //nolint:wrapcheck // the caller adds context
	})
	if err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// JSON writes the dataset as a JSON array, streamed rather than buffered so a
// dataset larger than memory still exports.
func JSON(ctx context.Context, s *store.Store, w io.Writer) error {
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return fmt.Errorf("export: %w", err)
	}
	enc := json.NewEncoder(w)
	first := true
	err := Rows(ctx, s, func(r Row) error {
		if !first {
			if _, werr := io.WriteString(w, ",\n"); werr != nil {
				return fmt.Errorf("export: %w", werr)
			}
		}
		first = false
		return enc.Encode(r) //nolint:wrapcheck // the caller adds context
	})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "]\n"); err != nil {
		return fmt.Errorf("export: %w", err)
	}
	return nil
}

func f(v float64, prec int) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', prec, 64)
}

func i(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func b(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// Filename suggests a name for a download, stamped so two exports do not
// silently overwrite each other in somebody's downloads folder.
func Filename(ext string, at time.Time) string {
	return fmt.Sprintf("scdnr-tags-%s.%s", at.UTC().Format("20060102"), ext)
}
