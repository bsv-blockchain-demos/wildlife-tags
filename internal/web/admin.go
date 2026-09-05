package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/auth"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/qr"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/service"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// requireAdmin gates an API route behind a session.
func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, *store.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.auth.Session(r.Context(), r)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		next(w, r, sess)
	}
}

// requireAdminPage gates a page, redirecting rather than returning JSON so a
// biologist who followed a bookmark lands on the login screen.
func (s *Server) requireAdminPage(next func(http.ResponseWriter, *http.Request, *store.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.auth.Session(r.Context(), r)
		if err != nil {
			if errors.Is(err, auth.ErrNoSession) || errors.Is(err, auth.ErrNotAdmin) {
				http.Redirect(w, r, "/admin", http.StatusSeeOther)
				return
			}
			s.writeErr(w, err)
			return
		}
		next(w, r, sess)
	}
}

func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	nonce, err := s.auth.Challenge()
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nonce":    nonce,
		"protocol": auth.AdminProtocol.Protocol,
		// The security level tells the page which BRC-100 protocol tuple to
		// sign under; getting it wrong produces a signature over a different
		// derived key that will simply not verify.
		"security_level": int(auth.AdminProtocol.SecurityLevel),
	})
}

type loginForm struct {
	IdentityKey string `json:"identity_key,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
	Signature   string `json:"signature,omitempty"`
	Password    string `json:"password,omitempty"`

	// Bearer asks for the session token in the response body, for a client
	// with no cookie jar -- which is every mobile app.
	//
	// It is opt-in rather than always returned because the cookie is HttpOnly,
	// and handing the same token to page JavaScript would throw that protection
	// away for every browser session to spare one field from the phone.
	Bearer bool `json:"bearer,omitempty"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var form loginForm
	if !decode(w, r, &form) {
		return
	}

	var (
		sess *store.Session
		err  error
	)
	if form.Password != "" {
		sess, err = s.auth.LoginWithPassword(r.Context(), form.Password)
	} else {
		sess, err = s.auth.LoginWithIdentity(r.Context(), form.IdentityKey, form.Nonce, form.Signature)
	}
	if err != nil {
		s.writeErr(w, err)
		return
	}

	s.auth.SetCookie(w, sess)
	body := map[string]any{
		"identity_key": sess.IdentityKey,
		"expires_at":   sess.ExpiresAt,
	}
	if form.Bearer {
		body["token"] = sess.Token
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r.Context(), r); err != nil {
		s.logger.Warn("logout", "error", err)
	}
	s.auth.ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	writeJSON(w, http.StatusOK, map[string]any{
		"identity_key": sess.IdentityKey,
		"label":        sess.Label,
		"expires_at":   sess.ExpiresAt,
	})
}

func (s *Server) handleFunding(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	addr, err := s.svc.Chain().DepositAddress()
	if err != nil {
		s.writeErr(w, err)
		return
	}
	balance, err := s.svc.Chain().Balance(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	cfg := s.svc.Config()
	left := uint64(0)
	if cfg.TotalReward() > 0 {
		left = balance / cfg.TotalReward()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deposit_address":  addr,
		"balance":          balance,
		"activations_left": left,
		"reward_per_tag":   cfg.TotalReward(),
	})
}

func (s *Server) handleBatches(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	batches, err := s.svc.Store().ListBatches(r.Context(), 50)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batches": batches})
}

type mintForm struct {
	Count   int    `json:"count"`
	Species string `json:"species"`
}

func (s *Server) handleMintBatch(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	var form mintForm
	if !decode(w, r, &form) {
		return
	}
	batch, tags, err := s.svc.MintBatch(r.Context(), form.Count, form.Species, sess.IdentityKey)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batch": batch, "tags": publicTags(tags)})
}

func (s *Server) handleAdminTags(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	limit := intParam(r, "limit", 200, 1, 1000)
	status := store.Status(r.URL.Query().Get("status"))
	tags, err := s.svc.Store().ListTags(r.Context(), status, limit, 0)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": publicTags(tags)})
}

// activateForm is what the tagging page submits at both steps.
//
// Meas and Attr are the species profile's fields, so the form is whatever
// GET /api/schema said the chosen species records.
type activateForm struct {
	TagID     string            `json:"tag_id"`
	Species   string            `json:"species"`
	Lat       float64           `json:"lat"`
	Lon       float64           `json:"lon"`
	AccuracyM float64           `json:"accuracy_m"`
	Meas      map[string]int    `json:"meas"`
	Attr      map[string]string `json:"attr"`
	Name      string            `json:"name,omitempty"`

	Observation  string `json:"observation,omitempty"`
	AttestSig    string `json:"attest_sig,omitempty"`
	AttestPubHex string `json:"attest_pub,omitempty"`
}

func (f activateForm) input(id tagkey.ID) service.ActivationInput {
	return service.ActivationInput{
		TagID:        id,
		Species:      f.Species,
		AttestPubHex: f.AttestPubHex,
		Lat:          f.Lat,
		Lon:          f.Lon,
		AccuracyM:    f.AccuracyM,
		Meas:         f.Meas,
		Attr:         f.Attr,
		Name:         f.Name,
	}
}

// handleActivatePrepare returns the canonical bytes the biologist's wallet
// signs. Two round trips, so the record is attributable to them rather than to
// whoever runs the server.
func (s *Server) handleActivatePrepare(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	var form activateForm
	if !decode(w, r, &form) {
		return
	}
	id, err := tagkey.ParseID(form.TagID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	in := form.input(id)
	if in.AttestPubHex == "" {
		// Fall back to the signed-in identity, so a console session that has
		// not separately fetched its key still produces an attributable record.
		in.AttestPubHex = sess.IdentityKey
	}
	preview, err := s.svc.PrepareActivation(r.Context(), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// The canonical tag id, echoed back so the client signs under exactly
		// the key id the server will verify with. A biologist types the
		// displayed form -- "JTZ-DQT3" -- and ParseID strips the dash; signing
		// under the typed string would derive a different key and the
		// attestation would fail with nothing on screen to explain why.
		"tag_id":         string(id),
		"species":        preview.Species,
		"observation":    hexOf(preview.Observation),
		"at":             preview.At,
		"base_satoshis":  preview.BaseSats,
		"bonus_satoshis": preview.BonusSats,
		"total_satoshis": preview.TotalSats,
	})
}

func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	var form activateForm
	if !decode(w, r, &form) {
		return
	}
	id, err := tagkey.ParseID(form.TagID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	observation, err := decodeHex(form.Observation)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	sig, err := decodeHex(form.AttestSig)
	if err != nil {
		s.writeErr(w, err)
		return
	}

	in := form.input(id)
	in.Observation, in.AttestSig, in.AttestPubHex = observation, sig, form.AttestPubHex
	if in.AttestPubHex == "" {
		in.AttestPubHex = sess.IdentityKey
	}

	res, err := s.svc.Activate(r.Context(), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"txid":        res.TxID,
		"vout":        res.Vout,
		"satoshis":    res.Satoshis,
		"sweep_after": res.SweepAfter,
	})
}

type rearmForm struct {
	TagID string `json:"tag_id"`
}

func (s *Server) handleRearm(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	var form rearmForm
	if !decode(w, r, &form) {
		return
	}
	id, err := tagkey.ParseID(form.TagID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if err := s.svc.Rearm(r.Context(), string(id), sess.IdentityKey); err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "back in service"})
}

// handlePrintSheet renders a batch's printable QR sheet.
//
// It is served with no-store because every code on the page can redeem a tag: a
// cached copy in a shared browser, or in a proxy, is a stack of bearer
// instruments sitting somewhere nobody is thinking about.
func (s *Server) handlePrintSheet(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	batchID := r.PathValue("batchID")
	batch, err := s.svc.Store().GetBatch(r.Context(), batchID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	tags, err := s.svc.Store().TagsByBatch(r.Context(), batchID)
	if err != nil {
		s.writeErr(w, err)
		return
	}

	// The sheet's own label for the species -- see qr.Sheet.SpeciesCommon --
	// falls back to the raw code rather than failing the print outright if
	// the profile can't be found, since a bad lookup here should not be able
	// to block printing a sheet whose codes are otherwise perfectly valid.
	speciesCommon := batch.Species
	if profile, perr := species.Get(batch.Species); perr == nil {
		speciesCommon = profile.Common
	}

	cfg := s.svc.Config()
	sheet := qr.Sheet{
		BatchID:       batch.ID,
		CreatedAt:     batch.CreatedAt.Format(time.RFC1123),
		PublicURL:     cfg.PublicURL,
		SpeciesCommon: speciesCommon,
		SpeciesUpper:  strings.ToUpper(speciesCommon),
	}
	for i, t := range tags {
		secret, serr := s.svc.SecretFor(t.Ordinal)
		if serr != nil {
			s.writeErr(w, serr)
			return
		}
		id := tagkey.ID(t.TagID)
		payload := s.svc.QRPayload(id, secret)
		code, cerr := qr.Encode(payload)
		if cerr != nil {
			s.writeErr(w, cerr)
			return
		}
		sheet.Tags = append(sheet.Tags, qr.SheetTag{
			TagID: t.TagID, Display: id.Display(), Ordinal: t.Ordinal,
			Payload: payload, Code: code, Position: i + 1,
		})
	}

	if err := s.svc.Store().Audit(r.Context(), sess.IdentityKey, "batch.print", batch.ID); err != nil {
		s.logger.Warn("could not record a print in the audit log", "batch", batch.ID, "error", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := qr.Render(w, sheet); err != nil {
		s.logger.Error("print sheet failed midway", "batch", batch.ID, "error", err)
	}
}

func hexOf(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexdigits[c>>4]
		out[i*2+1] = hexdigits[c&0x0f]
	}
	return string(out)
}
