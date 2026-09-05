// Package web serves the three faces of the program: a public dashboard, the
// page a scanned QR code lands on, and the console a biologist arms tags from.
//
// Plain net/http with Go 1.22 method patterns. No framework and no middleware
// chain, matching the sibling applications: cross-cutting concerns here are few
// enough to be visible at each handler, and a chain would hide the one that
// matters (which routes require a session).
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/auth"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/service"
)

//go:embed static
var staticFS embed.FS

// Server is the HTTP surface.
type Server struct {
	svc    *service.Service
	auth   *auth.Authenticator
	logger *slog.Logger
	now    func() time.Time

	static      fs.FS
	etags       map[string]string
	indexOnce   sync.Once
	indexBodies map[string][]byte

	// redeemLimiter throttles redemption attempts.
	//
	// Global rather than per-IP, and that is deliberate: behind an ingress
	// RemoteAddr is the proxy, and X-Forwarded-For is written by the caller. A
	// per-IP bucket keyed on either is a bucket the attacker chooses the key
	// for.
	redeemLimiter *bucket
}

// New builds the server.
func New(svc *service.Service, a *auth.Authenticator, logger *slog.Logger) (*Server, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("web: mount static assets: %w", err)
	}

	s := &Server{
		svc:           svc,
		auth:          a,
		logger:        logger,
		now:           func() time.Time { return time.Now().UTC() },
		static:        sub,
		redeemLimiter: newBucket(20, 3*time.Second),
	}
	if err := s.hashAssets(); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/tags", s.handleTags)
	mux.HandleFunc("GET /api/tag/{tagID}", s.handleTag)
	mux.HandleFunc("GET /api/info", s.handleInfo)
	// The species profiles. Every form on every client is built from this, and
	// it is cacheable so a phone out of signal still knows what to ask for.
	mux.HandleFunc("GET /api/schema", s.handleSchema)
	mux.HandleFunc("GET /api/export.csv", s.handleExportCSV)
	mux.HandleFunc("GET /api/export.json", s.handleExportJSON)

	// Redemption. Three steps, because the finder's wallet signs the record
	// and the tag key signs the transaction, and neither can be done by the
	// server on their behalf.
	mux.HandleFunc("POST /api/redeem/quote", s.handleRedeemQuote)
	mux.HandleFunc("POST /api/redeem/prepare", s.handleRedeemPrepare)
	mux.HandleFunc("POST /api/redeem/complete", s.handleRedeemComplete)

	// Admin.
	mux.HandleFunc("POST /api/admin/challenge", s.handleChallenge)
	mux.HandleFunc("POST /api/admin/login", s.handleLogin)
	mux.HandleFunc("POST /api/admin/logout", s.handleLogout)
	mux.HandleFunc("GET /api/admin/session", s.requireAdmin(s.handleSession))
	mux.HandleFunc("GET /api/admin/funding", s.requireAdmin(s.handleFunding))
	mux.HandleFunc("GET /api/admin/batches", s.requireAdmin(s.handleBatches))
	mux.HandleFunc("POST /api/admin/batches", s.requireAdmin(s.handleMintBatch))
	mux.HandleFunc("GET /api/admin/tags", s.requireAdmin(s.handleAdminTags))
	mux.HandleFunc("POST /api/admin/activate/prepare", s.requireAdmin(s.handleActivatePrepare))
	mux.HandleFunc("POST /api/admin/activate", s.requireAdmin(s.handleActivate))
	mux.HandleFunc("POST /api/admin/rearm", s.requireAdmin(s.handleRearm))
	mux.HandleFunc("GET /admin/batches/{batchID}/print", s.requireAdminPage(s.handlePrintSheet))

	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)

	// Pages. /t/{tagID} is where a scanned QR lands; the fragment carrying the
	// secret never reaches the server, which is the whole reason it is a
	// fragment.
	mux.HandleFunc("GET /t/{tagID}", s.servePage("redeem.html"))
	mux.HandleFunc("GET /about", s.servePage("about.html"))
	mux.HandleFunc("GET /admin", s.servePage("admin.html"))
	mux.HandleFunc("GET /", s.serveStatic)

	return mux
}

// hashAssets computes an ETag per embedded file.
//
// Embedded files carry a zero modification time, so net/http sends no
// Last-Modified and its FileServer never sets an ETag -- every asset is then
// re-fetched in full on every load, which on a phone with one bar of signal in
// a marsh is the difference between a page that works and one that does not.
func (s *Server) hashAssets() error {
	s.etags = make(map[string]string)
	s.indexBodies = make(map[string][]byte)

	return fs.WalkDir(s.static, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, rerr := fs.ReadFile(s.static, path)
		if rerr != nil {
			return fmt.Errorf("web: read %s: %w", path, rerr)
		}
		sum := sha256.Sum256(body)
		s.etags[path] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
}

// assetRef matches a src= or href= pointing at one of our own assets. Matching
// the attribute rather than the bare filename means prose mentioning "app.js"
// is left alone.
var assetRef = regexp.MustCompile(`(src|href)="(/?[a-zA-Z0-9_.\-/]+\.(?:js|css))"`)

// pageBody serves an HTML page with its asset references stamped by content
// hash.
//
// The stamp is in the URL rather than a header because a header is advisory: a
// CDN in front of this will happily rewrite Cache-Control and serve its own
// copy of a stale script, and a URL is not something it can second-guess.
func (s *Server) pageBody(name string) ([]byte, string, error) {
	s.indexOnce.Do(func() {
		for path := range s.etags {
			if !strings.HasSuffix(path, ".html") {
				continue
			}
			body, err := fs.ReadFile(s.static, path)
			if err != nil {
				continue
			}
			stamped := assetRef.ReplaceAllFunc(body, func(m []byte) []byte {
				groups := assetRef.FindSubmatch(m)
				ref := strings.TrimPrefix(string(groups[2]), "/")
				tag, ok := s.etags[ref]
				if !ok {
					return m
				}
				return []byte(fmt.Sprintf(`%s="/%s?v=%s"`, groups[1], ref, strings.Trim(tag, `"`)))
			})
			s.indexBodies[path] = stamped
		}
	})

	if body, ok := s.indexBodies[name]; ok {
		return body, s.etags[name], nil
	}
	body, err := fs.ReadFile(s.static, name)
	if err != nil {
		return nil, "", fmt.Errorf("web: read %s: %w", name, err)
	}
	return body, s.etags[name], nil
}

func (s *Server) servePage(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, etag, err := s.pageBody(name)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	}
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" || path == "index.html" {
		s.servePage("index.html")(w, r)
		return
	}

	body, err := fs.ReadFile(s.static, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	etag := s.etags[path]
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	switch {
	case strings.HasSuffix(path, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(path, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(path, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(path, ".svg"):
		// Without this, Go's fallback content sniffer reads the XML
		// declaration every one of these files starts with and calls it
		// text/xml, which a browser will not paint into an <img>: the
		// species icons render as a broken-image glyph instead of a crab.
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(path, ".woff2"):
		w.Header().Set("Content-Type", "font/woff2")
	}
	w.Header().Set("ETag", etag)
	if r.URL.Query().Get("v") != "" {
		// Content-addressed, so it can be cached hard.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	_, _ = w.Write(body)
}
