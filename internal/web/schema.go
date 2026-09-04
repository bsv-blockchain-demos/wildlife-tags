package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
)

// handleSchema publishes every species profile the deployment knows about.
//
// This is what makes the clients species-agnostic rather than merely
// re-hardcoding one animal somewhere else. Before it existed, "M/FI/FM/FS",
// "HARD/PEELER_*", the 127 mm minimum and the 50-260 mm plausible range were
// written out four times -- in redeem.html, admin.html, redeem.js and admin.js
// -- and adding a species meant finding all four and a Go file besides.
//
// It carries an ETag so a phone can cache it and keep working with no signal.
// A field app that cannot render a form until it reaches the server is a field
// app that does not work in a marsh, which is where it is used.
func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	body, etag, err := schemaDocument()
	if err != nil {
		s.writeErr(w, err)
		return
	}

	w.Header().Set("ETag", etag)
	// Revalidate rather than cache blindly: a profile change is a legal or
	// protocol change, and a client showing last month's size limit would be
	// telling somebody it is legal to keep an undersized animal.
	w.Header().Set("Cache-Control", "no-cache")
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if _, err := w.Write(body); err != nil {
		s.logger.Warn("schema response failed midway", "error", err)
	}
}

var (
	schemaOnce sync.Once
	schemaBody []byte
	schemaTag  string
	schemaErr  error
)

// schemaDocument renders the profiles once. They are embedded at build time and
// cannot change while the process runs, so the ETag is stable for the life of a
// deployment and a client's cached copy stays valid across restarts of the same
// binary.
func schemaDocument() ([]byte, string, error) {
	schemaOnce.Do(func() {
		profiles, err := species.All()
		if err != nil {
			schemaErr = err
			return
		}
		body, err := json.Marshal(map[string]any{
			"default":  species.Default,
			"profiles": profiles,
			// Named here rather than assumed by each client, because a client
			// that guesses this key wrong builds a form with no disposition on
			// it and every report it makes is refused.
			"disposition_key": species.DispositionKey,
		})
		if err != nil {
			schemaErr = err
			return
		}
		sum := sha256.Sum256(body)
		schemaBody, schemaTag = body, `"`+hex.EncodeToString(sum[:8])+`"`
	})
	return schemaBody, schemaTag, schemaErr
}
