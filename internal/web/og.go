package web

import (
	"bytes"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"strings"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/ogimage"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// speciesArt is the gradient and icon a browser would show for a species --
// the same pairing as style.css's --grad-* tokens and the SPECIES_ICON /
// SPECIES_GRADIENT maps in admin.js and redeem.js. There is no module
// system shared between the Go server and the two places this is
// duplicated in JavaScript, so a new species profile needs an entry here
// too, or its tag pages fall back to the generic crab silhouette rather
// than failing outright.
type speciesArt struct {
	From, To string
	Icon     string
}

var speciesArtByCode = map[string]speciesArt{
	"CALSAP": {From: "#0f5c56", To: "#2dd4bf", Icon: "blue-crab"},
	"SCIOCE": {From: "#9a3412", To: "#fb923c", Icon: "red-drum"},
}

var fallbackArt = speciesArt{From: "#1e293b", To: "#94a3b8", Icon: "crab-generic"}

func artFor(code string) speciesArt {
	if a, ok := speciesArtByCode[code]; ok {
		return a
	}
	return fallbackArt
}

// ogIcons is every icon key ogimage.Card.IconKey can reference: the two
// real species, the five family-level fallbacks vendored alongside them,
// and "tag", the brand mark used wherever there is no animal to show yet.
var ogIcons = []string{"tag", "blue-crab", "red-drum", "crab-generic", "fish-generic", "sea-turtle", "shark", "bird-generic"}

// newOGRenderer loads the fonts and pre-rendered icon PNGs an
// ogimage.Renderer needs from the same embedded filesystem everything else
// is served from, so there is exactly one copy of each to keep in sync.
func newOGRenderer(static fs.FS) (*ogimage.Renderer, error) {
	displayTTF, err := fs.ReadFile(static, "vendor/fonts/google-sans-flex-latin.ttf")
	if err != nil {
		return nil, fmt.Errorf("read display font: %w", err)
	}
	bodyTTF, err := fs.ReadFile(static, "vendor/fonts/roboto-latin.ttf")
	if err != nil {
		return nil, fmt.Errorf("read body font: %w", err)
	}
	icons := make(map[string][]byte, len(ogIcons))
	for _, name := range ogIcons {
		b, err := fs.ReadFile(static, "vendor/animals/og/"+name+"-white.png")
		if err != nil {
			return nil, fmt.Errorf("read icon %s: %w", name, err)
		}
		icons[name] = b
	}
	return ogimage.New(displayTTF, bodyTTF, icons)
}

// writeOG renders a card as a PNG response. An hour of caching is a
// judgment call: long enough that a chat client re-fetching the same link a
// few times in a conversation does not regenerate it every time, short
// enough that a crab getting a name today shows up in tomorrow's share
// preview without anyone having to think about cache invalidation.
func (s *Server) writeOG(w http.ResponseWriter, card ogimage.Card) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := s.og.Render(w, card); err != nil {
		s.logger.Error("og image render failed", "error", err)
	}
}

func (s *Server) handleOGHome(w http.ResponseWriter, r *http.Request) {
	s.writeOG(w, ogimage.Card{
		GradientFrom: brandGradFrom, GradientTo: brandGradTo, IconKey: "tag",
		Eyebrow:  "SCDNR Wildlife Tags",
		Title:    "Scan a tag. Report it. Get paid on the spot.",
		Subtitle: "A reward locked to the tag itself, on Bitcoin SV.",
	})
}

func (s *Server) handleOGAbout(w http.ResponseWriter, r *http.Request) {
	s.writeOG(w, ogimage.Card{
		GradientFrom: "#4c1d95", GradientTo: "#c4b5fd", IconKey: "tag",
		Eyebrow:  "About the programme",
		Title:    "What this changes, and what it costs to try.",
		Subtitle: "The reward and the record are the same object.",
	})
}

const brandGradFrom, brandGradTo = "#0f5c56", "#2dd4bf"

// handleOGTag is the generative image: what it draws depends entirely on
// this one tag's own recorded history, not a template filled in with a
// generic message. A tag with no story yet (never armed, or armed but
// never found) gets an honest, simpler card rather than fabricated stats.
func (s *Server) handleOGTag(w http.ResponseWriter, r *http.Request) {
	id, err := tagkey.ParseID(r.PathValue("tagID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tag, err := s.svc.Store().GetTag(r.Context(), string(id))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	art := artFor(tag.Species)
	card := ogimage.Card{GradientFrom: art.From, GradientTo: art.To, IconKey: art.Icon}

	prov, provErr := s.svc.Provenance(r.Context(), string(id))
	if provErr == nil && prov != nil && prov.TaggedAt != nil {
		card.Eyebrow = prov.Common
		card.Subtitle = "Tag " + id.Display()
		if prov.Name != "" {
			card.Title = prov.Name
		} else {
			card.Title = "An unnamed find"
		}
		card.Stats = []ogimage.Stat{
			{Value: fmt.Sprintf("%d", prov.DaysAtLarge), Label: dayWord(prov.DaysAtLarge)},
			{Value: distanceLabel(prov.DistanceM), Label: "travelled"},
			{Value: fmt.Sprintf("%d", len(prov.Recaptures)+1), Label: sightingWord(len(prov.Recaptures) + 1)},
		}
	} else {
		card.Eyebrow = "SCDNR Wildlife Tags"
		card.Title = "Report this tag, get paid on the spot"
		card.Subtitle = "Tag " + id.Display()
	}

	s.writeOG(w, card)
}

func dayWord(n int) string {
	if n == 1 {
		return "day"
	}
	return "days"
}

func sightingWord(n int) string {
	if n == 1 {
		return "sighting"
	}
	return "sightings"
}

// distanceLabel matches the units the journey stats on the redemption page
// itself switch at (see redeem.js's renderJourney): metres under a
// kilometre, one decimal of kilometres above it.
func distanceLabel(m int) string {
	if m < 1000 {
		return fmt.Sprintf("%d m", m)
	}
	return fmt.Sprintf("%.1f km", float64(m)/1000)
}

// og placeholder tokens in redeem.html's <head>. Plain byte substitution,
// not a template engine: this is the one page whose <title>/og:* content
// depends on the request rather than being fixed at build time, and three
// find-and-replace calls are simpler than pulling in html/template for a
// single page that has no other need of it.
const (
	ogTitleToken = "__OG_TITLE__"
	ogDescToken  = "__OG_DESC__"
	ogImageToken = "__OG_IMAGE__"
)

// handleRedeemPage serves redeem.html with its share-preview title,
// description, and image filled in for the specific tag in the URL --
// pageBody's cached, asset-stamped HTML with three tokens swapped for real
// values, not a full re-render. A tag that fails to parse or look up still
// gets the page (the client-side script has its own "tag not found"
// handling); it just falls back to the generic wildtag preview rather than
// a broken one.
func (s *Server) handleRedeemPage(w http.ResponseWriter, r *http.Request) {
	body, _, err := s.pageBody("redeem.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	publicURL := s.svc.Config().PublicURL
	title := "Tagged animal: report and get paid"
	desc := "Report a tagged animal and get paid on the spot."
	imagePath := "/api/og.png"

	if id, perr := tagkey.ParseID(r.PathValue("tagID")); perr == nil {
		imagePath = "/api/og/tag/" + string(id)

		if prov, provErr := s.svc.Provenance(r.Context(), string(id)); provErr == nil && prov != nil && prov.TaggedAt != nil {
			name := prov.Name
			if name == "" {
				name = "An unnamed " + strings.ToLower(prov.Common)
			}
			title = fmt.Sprintf("%s: report and get paid", name)
			desc = fmt.Sprintf("%s, tag %s. Scan it to report and get paid on the spot.", prov.Common, id.Display())
		}
	}

	body = bytes.ReplaceAll(body, []byte(ogTitleToken), []byte(html.EscapeString(title)))
	body = bytes.ReplaceAll(body, []byte(ogDescToken), []byte(html.EscapeString(desc)))
	body = bytes.ReplaceAll(body, []byte(ogImageToken), []byte(html.EscapeString(publicURL+imagePath)))

	// Not ETag-cached like servePage's other pages: the body is now
	// per-request, and a finder's phone should always see this tag's own
	// preview rather than a 304 pointing at whichever tag asked first.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}
