package web

import (
	"net/http"
	"sync"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/service"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// recaptureForm is what the redemption page submits at every step.
//
// Meas and Attr are the species profile's fields rather than named columns, so
// a page driven by GET /api/schema submits whatever that profile declares and
// this struct never has to learn about a new animal.
type recaptureForm struct {
	TagID       string            `json:"tag_id"`
	Lat         float64           `json:"lat"`
	Lon         float64           `json:"lon"`
	AccuracyM   float64           `json:"accuracy_m"`
	Meas        map[string]int    `json:"meas"`
	Attr        map[string]string `json:"attr"`
	PayeePubHex string            `json:"payee"`
	Name        string            `json:"name,omitempty"`

	// Observation and AttestSig appear from the prepare step onwards, once the
	// finder's wallet has signed the quote.
	Observation  string `json:"observation,omitempty"`
	AttestSig    string `json:"attest_sig,omitempty"`
	AttestPubHex string `json:"attest_pub,omitempty"`
}

func (f recaptureForm) details() service.RecaptureDetails {
	return service.RecaptureDetails{
		Lat:         f.Lat,
		Lon:         f.Lon,
		AccuracyM:   f.AccuracyM,
		Meas:        f.Meas,
		Attr:        f.Attr,
		PayeePubHex: f.PayeePubHex,
		Name:        f.Name,
	}
}

// handleRedeemQuote returns what the crabber is offered and the exact bytes
// their wallet must sign.
//
// It is a separate round trip because a BRC-100 wallet signs bytes it is handed,
// and the server cannot hand them over until it knows what the crabber caught.
func (s *Server) handleRedeemQuote(w http.ResponseWriter, r *http.Request) {
	var form recaptureForm
	if !decode(w, r, &form) {
		return
	}
	id, err := tagkey.ParseID(form.TagID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	quote, err := s.svc.QuoteRecapture(r.Context(), string(id), form.details())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

// handleRedeemPrepare builds the unsigned redemption.
//
// Rate limited and serialised per tag. The tag claim inside the service is what
// actually prevents a double spend; this bucket is about not letting an
// automated caller pin the wallet's inputs.
func (s *Server) handleRedeemPrepare(w http.ResponseWriter, r *http.Request) {
	if !s.redeemLimiter.take(s.now()) {
		w.Header().Set("Retry-After", "3")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "too many redemptions at once; wait a moment and scan again",
		})
		return
	}

	var form recaptureForm
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

	draft, err := s.svc.PrepareRedeem(r.Context(), service.RedeemInput{
		TagID:        id,
		Lat:          form.Lat,
		Lon:          form.Lon,
		AccuracyM:    form.AccuracyM,
		Meas:         form.Meas,
		Attr:         form.Attr,
		PayeePubHex:  form.PayeePubHex,
		Name:         form.Name,
		Observation:  observation,
		AttestSig:    sig,
		AttestPubHex: form.AttestPubHex,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

type completeForm struct {
	Reference string `json:"reference"`
	TagSig    string `json:"tag_sig"`
}

// handleRedeemComplete takes the tag-key signature the browser made and pays
// the crabber.
func (s *Server) handleRedeemComplete(w http.ResponseWriter, r *http.Request) {
	var form completeForm
	if !decode(w, r, &form) {
		return
	}
	sig, err := decodeHex(form.TagSig)
	if err != nil {
		s.writeErr(w, err)
		return
	}

	receipt, err := s.svc.CompleteRedeem(r.Context(), form.Reference, sig)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

// bucket is a token bucket.
//
// Global, not per-IP: behind an ingress RemoteAddr is the proxy and
// X-Forwarded-For is written by the caller, so a per-IP bucket keyed on either
// is one whose key the attacker picks.
type bucket struct {
	mu       sync.Mutex
	tokens   int
	capacity int
	interval time.Duration
	last     time.Time
}

func newBucket(capacity int, interval time.Duration) *bucket {
	return &bucket{tokens: capacity, capacity: capacity, interval: interval}
}

func (b *bucket) take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.last.IsZero() {
		b.last = now
	}
	if refill := int(now.Sub(b.last) / b.interval); refill > 0 {
		b.tokens = min(b.capacity, b.tokens+refill)
		b.last = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}
