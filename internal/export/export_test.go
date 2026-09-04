package export

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
)

func seeded(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tags.db"), "")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC()
	if err := s.CreateBatch(t.Context(), store.Batch{
		ID: "B1", CreatedAt: now, CreatedBy: "02bio", TagCount: 1, ManifestHash: "abc",
	}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if err := s.InsertTags(t.Context(), []store.Tag{{
		TagID: "K2M9Q7C", BatchID: "B1", Ordinal: 0, PubKeyHex: "02aa", CreatedAt: now,
	}}); err != nil {
		t.Fatalf("tags: %v", err)
	}

	act, _ := record.Marshal(record.Observation{
		AccCM: 480,
		Attr:  map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP"},
		LatE7: record.EncodeCoord(32.7765), LonE7: record.EncodeCoord(-79.9311),
		Meas: map[string]int{"cw": 142, "wt": 2840, "sal": 3120},
		Obs:  "02bio", Sp: "CALSAP", TS: "2026-05-01T12:00:00Z",
	})
	actSet, _ := record.Marshal(record.Settlement{BaseSat: 5000, Batch: "B1", BonSat: 15000})
	id, _ := s.BeginEvent(t.Context(), store.Event{
		TagID: "K2M9Q7C", Generation: 0, Kind: "ACT",
		PayloadJSON: string(act), SettlementJSON: string(actSet), AttestPubKey: "02bio",
	})
	_ = s.BroadcastEvent(t.Context(), id, "aa11", 0, 20000)
	_ = s.MarkEventMined(t.Context(), "aa11")

	rec, _ := record.Marshal(record.Observation{
		AccCM: 620,
		Attr:  map[string]string{"sex": "M", "gear": "TROTLINE", "disp": "RELEASED"},
		LatE7: record.EncodeCoord(32.83), LonE7: record.EncodeCoord(-79.84),
		Meas: map[string]int{"cw": 149},
		Obs:  "02finder", Sp: "CALSAP", TS: "2026-08-06T09:00:00Z",
	})
	recSet, _ := record.Marshal(record.Settlement{
		DaysAt: 97, DistM: 14200, PaidSat: 5000, Payee: "02finder", Prev: "aa11", QueueSec: 240,
	})
	id2, _ := s.BeginEvent(t.Context(), store.Event{
		TagID: "K2M9Q7C", Generation: 1, Kind: "REC",
		PayloadJSON: string(rec), SettlementJSON: string(recSet), AttestPubKey: "02finder",
	})
	_ = s.BroadcastEvent(t.Context(), id2, "bb22", 0, 5000)

	// A failed attempt, which must not appear: it recorded nothing that
	// happened, and a phantom animal in a movement study is worse than a gap.
	id3, _ := s.BeginEvent(t.Context(), store.Event{
		TagID: "K2M9Q7C", Generation: 2, Kind: "REC",
		PayloadJSON: string(rec), SettlementJSON: string(recSet), AttestPubKey: "02ghost",
	})
	_ = s.FailEvent(t.Context(), id3, "broadcast rejected")

	return s
}

func TestTheDatasetCarriesBothEvents(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(t.Context(), seeded(t), &buf); err != nil {
		t.Fatalf("csv: %v", err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows including the header, want 3", len(rows))
	}
	if rows[1][1] != "tagged" || rows[2][1] != "recaptured" {
		t.Fatalf("events are %q and %q", rows[1][1], rows[2][1])
	}
}

// TestAFailedTransactionIsNotADataPoint keeps phantom animals out of a movement
// study.
func TestAFailedTransactionIsNotADataPoint(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(t.Context(), seeded(t), &buf); err != nil {
		t.Fatalf("csv: %v", err)
	}
	if strings.Contains(buf.String(), "02ghost") {
		t.Fatal("a failed transaction appears in the public dataset")
	}
}

// TestEveryRowCanBeCheckedAgainstTheChain is the property that makes this an
// open dataset rather than a spreadsheet somebody has to believe.
func TestEveryRowCanBeCheckedAgainstTheChain(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(t.Context(), seeded(t), &buf); err != nil {
		t.Fatalf("json: %v", err)
	}
	var rows []Row
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.TxID == "" {
			t.Errorf("%s row has no transaction to check against", r.Event)
		}
	}

	// "Proven" must mean a verified merkle proof, not merely accepted.
	var tagged, caught Row
	for _, r := range rows {
		if r.Event == "tagged" {
			tagged = r
		} else {
			caught = r
		}
	}
	if !tagged.Proven {
		t.Error("a mined event is not marked proven")
	}
	if caught.Proven {
		t.Error("a merely-broadcast event is marked proven; that would overstate what is known")
	}
}

func TestScaledIntegersComeBackAsRealUnits(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(t.Context(), seeded(t), &buf); err != nil {
		t.Fatalf("json: %v", err)
	}
	var rows []Row
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, r := range rows {
		if r.Event != "tagged" {
			continue
		}
		if diff := r.Lat - 32.7765; diff > 1e-6 || diff < -1e-6 {
			t.Errorf("latitude came back as %.7f", r.Lat)
		}
		if r.AccuracyM != 4.8 {
			t.Errorf("accuracy came back as %v m, want 4.8", r.AccuracyM)
		}
		// Measurements stay as the scaled integers they were signed as; a
		// consumer scales them with the profile from GET /api/schema, which is
		// the only place that knows a "wt" of 2840 means 28.40 degrees.
		if r.Meas["wt"] != 2840 {
			t.Errorf("water temperature came back as %v, want the scaled 2840", r.Meas["wt"])
		}
		if r.Meas["sal"] != 3120 {
			t.Errorf("salinity came back as %v, want the scaled 3120", r.Meas["sal"])
		}
	}
}

func TestFilenameIsStamped(t *testing.T) {
	// Two exports in one day should not silently overwrite each other in a
	// downloads folder.
	at := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	if got := Filename("csv", at); got != "scdnr-tags-20260826.csv" {
		t.Fatalf("filename is %q", got)
	}
}

// TestTheColumnsFollowTheData is the property that makes this dataset
// species-agnostic: a red drum export must not carry empty carapace-width
// columns, and adding a species must not need a release here.
func TestTheColumnsFollowTheData(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(t.Context(), seeded(t), &buf); err != nil {
		t.Fatalf("csv: %v", err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}

	header := rows[0]
	index := map[string]int{}
	for i, h := range header {
		index[h] = i
	}
	for _, want := range []string{"meas_cw", "meas_wt", "meas_sal", "attr_sex", "attr_stage", "attr_gear", "attr_disp"} {
		if _, ok := index[want]; !ok {
			t.Errorf("the header has no %q column: %v", want, header)
		}
	}
	for _, unwanted := range []string{"meas_tl", "carapace_width_mm", "molt_stage", "sponge"} {
		if _, ok := index[unwanted]; ok {
			t.Errorf("the header carries %q, which nothing in this dataset records", unwanted)
		}
	}

	tagged, caught := rows[1], rows[2]
	if tagged[index["meas_cw"]] != "142" {
		t.Errorf("the tagging row records a width of %q", tagged[index["meas_cw"]])
	}
	if caught[index["attr_disp"]] != "RELEASED" {
		t.Errorf("the recapture row records a disposition of %q", caught[index["attr_disp"]])
	}
	// A measurement nobody took must read as blank, not as zero: a movement
	// study that cannot tell them apart has invented data in it.
	if got := caught[index["meas_wt"]]; got != "" {
		t.Errorf("an unrecorded water temperature reads as %q, not blank", got)
	}
	if got := caught[index["queued_seconds"]]; got != "240" {
		t.Errorf("the queue delay reads as %q; an offline capture must be visible in the dataset", got)
	}
}
