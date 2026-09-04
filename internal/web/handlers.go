package web

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/auth"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/export"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/service"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// maxBody bounds a request. The largest legitimate one is a redemption
// carrying a BEEF-derived signature, which is well under this.
const maxBody = 1 << 20

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already out; there is nothing left to say.
		_ = err
	}
}

// writeErr maps a domain error to a status a browser can act on.
//
// The mapping is deliberate rather than a blanket 500: a crabber standing in a
// marsh needs to know whether to try again, wait, or give up, and the message
// is the only thing telling them.
func (s *Server) writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, service.ErrTagBusy):
		code = http.StatusConflict
	case errors.Is(err, service.ErrCooldown), errors.Is(err, service.ErrTagNotActive):
		code = http.StatusConflict
	case errors.Is(err, service.ErrUnknownDraft):
		code = http.StatusGone
	case errors.Is(err, service.ErrPayloadMismatch):
		code = http.StatusBadRequest
	case errors.Is(err, auth.ErrNoSession):
		code = http.StatusUnauthorized
	case errors.Is(err, auth.ErrNotAdmin), errors.Is(err, auth.ErrBadSignature),
		errors.Is(err, auth.ErrBadPassword), errors.Is(err, auth.ErrBadChallenge):
		code = http.StatusForbidden
	case errors.Is(err, species.ErrMustRelease), errors.Is(err, species.ErrNotTaggable),
		errors.Is(err, species.ErrBadValue), errors.Is(err, species.ErrMissing),
		errors.Is(err, species.ErrBadCoordinate), errors.Is(err, species.ErrImplausibleAccuracy),
		errors.Is(err, species.ErrUnknownMeasure), errors.Is(err, species.ErrUnknownVocab),
		errors.Is(err, species.ErrBadName):
		code = http.StatusBadRequest
	case errors.Is(err, species.ErrUnknownSpecies):
		code = http.StatusNotFound
	case errors.Is(err, tagkey.ErrBadID), errors.Is(err, tagkey.ErrBadCheck),
		errors.Is(err, tagkey.ErrBadSecret):
		code = http.StatusBadRequest
	}
	if code == http.StatusInternalServerError {
		s.logger.Error("request failed", "error", err)
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request: " + err.Error()})
		return false
	}
	return true
}

// handleInfo is what every page loads first: the facts that do not change.
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	cfg := s.svc.Config()
	identity, err := s.svc.Chain().WalletIdentityKeyHex()
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"network": string(cfg.Network),
		// The network a client wallet should build itself for. Published rather
		// than left to the client to infer; see chain.Config.WalletChain.
		"wallet_chain":   cfg.WalletChain(),
		"arcade_url":     cfg.ArcadeURL,
		"public_url":     cfg.PublicURL,
		"identity_key":   identity,
		"base_satoshis":  cfg.BaseSatoshis,
		"bonus_satoshis": cfg.BonusSatoshis,
		"admin_protocol": auth.AdminProtocol.Protocol,
		"password_login": s.auth.PasswordConfigured(),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.svc.Store().Stats(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	paid, err := s.svc.Store().SatoshisPaid(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	stats.SatoshisPaid = paid

	events, err := s.svc.Store().RecentEvents(r.Context(), 25)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": stats, "recent": summarise(events)})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 100, 1, 500)
	offset := intParam(r, "offset", 0, 0, 1_000_000)
	status := store.Status(r.URL.Query().Get("status"))

	tags, err := s.svc.Store().ListTags(r.Context(), status, limit, offset)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": publicTags(tags)})
}

// handleTag is what the scanned page calls to find out what it is holding.
//
// It never returns anything secret. The tag id in the path is public; the
// secret lives in the URL fragment and never leaves the browser.
func (s *Server) handleTag(w http.ResponseWriter, r *http.Request) {
	id, err := tagkey.ParseID(r.PathValue("tagID"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	tag, err := s.svc.Store().GetTag(r.Context(), string(id))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	prov, err := s.svc.Provenance(r.Context(), string(id))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	cfg := s.svc.Config()

	writeJSON(w, http.StatusOK, map[string]any{
		"tag":            publicTag(*tag),
		"provenance":     prov,
		"base_satoshis":  cfg.BaseSatoshis,
		"bonus_satoshis": cfg.BonusSatoshis,
	})
}

func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+export.Filename("csv", s.now())+`"`)
	if err := export.CSV(r.Context(), s.svc.Store(), w); err != nil {
		s.logger.Error("csv export failed midway", "error", err)
	}
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+export.Filename("json", s.now())+`"`)
	if err := export.JSON(r.Context(), s.svc.Store(), w); err != nil {
		s.logger.Error("json export failed midway", "error", err)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady reports whether the program can actually do its job, which is
// not the same as being alive. A wallet with no money cannot arm a tag.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	balance, err := s.svc.Chain().Balance(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "error": err.Error()})
		return
	}
	cfg := s.svc.Config()
	fundsFor := uint64(0)
	if cfg.TotalReward() > 0 {
		fundsFor = balance / cfg.TotalReward()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ready":            true,
		"balance":          balance,
		"activations_left": fundsFor,
	})
}

// publicTag strips a tag down to what anyone may see.
//
// The ordinal is withheld. It is not a secret in the cryptographic sense --
// knowing it does not help without the master seed -- but publishing the exact
// derivation index of every tag narrows an attack on the seed for no benefit to
// anyone reading the dataset.
type publicTagView struct {
	TagID       string     `json:"tag_id"`
	Display     string     `json:"display"`
	Name        string     `json:"name,omitempty"`
	Status      string     `json:"status"`
	Generation  uint32     `json:"generation"`
	Satoshis    uint64     `json:"satoshis"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	LastEventAt *time.Time `json:"last_event_at,omitempty"`
	Cooldown    *time.Time `json:"cooldown_until,omitempty"`
	TxID        string     `json:"txid,omitempty"`
}

func publicTag(t store.Tag) publicTagView {
	return publicTagView{
		TagID:       t.TagID,
		Display:     tagkey.ID(t.TagID).Display(),
		Name:        t.AnimalName,
		Status:      string(t.Status),
		Generation:  t.Generation,
		Satoshis:    t.LiveSatoshis,
		ActivatedAt: t.ActivatedAt,
		LastEventAt: t.LastEventAt,
		Cooldown:    t.CooldownUntil,
		TxID:        t.LiveTxID,
	}
}

func publicTags(tags []store.Tag) []publicTagView {
	out := make([]publicTagView, 0, len(tags))
	for _, t := range tags {
		out = append(out, publicTag(t))
	}
	return out
}

type eventView struct {
	TagID      string    `json:"tag_id"`
	Display    string    `json:"display"`
	Kind       string    `json:"kind"`
	Generation uint32    `json:"generation"`
	TxID       string    `json:"txid"`
	Satoshis   uint64    `json:"satoshis"`
	Status     string    `json:"status"`
	At         time.Time `json:"at"`
}

func summarise(events []store.Event) []eventView {
	out := make([]eventView, 0, len(events))
	for _, e := range events {
		out = append(out, eventView{
			TagID:      e.TagID,
			Display:    tagkey.ID(e.TagID).Display(),
			Kind:       e.Kind,
			Generation: e.Generation,
			TxID:       e.TxID,
			Satoshis:   e.Satoshis,
			Status:     string(e.Status),
			At:         e.CreatedAt,
		})
	}
	return out
}

func intParam(r *http.Request, name string, def, lo, hi int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return def
	}
	return n
}

func decodeHex(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("not hex: %w", err)
	}
	return b, nil
}
