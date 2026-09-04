package species

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"sync"
)

//go:embed profiles/*.json
var profileFS embed.FS

var (
	loadOnce sync.Once
	loaded   map[string]*Profile
	loadErr  error
)

// load reads every embedded profile once, and refuses the lot if any is
// malformed.
//
// Failing at startup is deliberate. A profile with a rule naming a field it does
// not define is a legal constraint that silently never fires, and the only place
// that would surface is a report nobody realises should have been refused.
func load() {
	entries, err := profileFS.ReadDir("profiles")
	if err != nil {
		loadErr = fmt.Errorf("species: read profiles: %w", err)
		return
	}
	loaded = make(map[string]*Profile, len(entries))
	for _, e := range entries {
		body, rerr := profileFS.ReadFile(path.Join("profiles", e.Name()))
		if rerr != nil {
			loadErr = fmt.Errorf("species: read %s: %w", e.Name(), rerr)
			return
		}
		var p Profile
		if uerr := json.Unmarshal(body, &p); uerr != nil {
			loadErr = fmt.Errorf("species: parse %s: %w", e.Name(), uerr)
			return
		}
		// Every mark-recapture profile records what the finder did with the
		// animal, and none of them declare it -- see DispositionKey. Injected
		// before validation so a profile's rules may refer to it.
		if p.Workflow == MarkRecapture {
			p.Vocabs = append(p.Vocabs, dispositionVocab())
		}
		if verr := p.Validate(); verr != nil {
			loadErr = fmt.Errorf("species: %s: %w", e.Name(), verr)
			return
		}
		if _, dup := loaded[p.Code]; dup {
			loadErr = fmt.Errorf("%w: two profiles claim code %s", ErrBadProfile, p.Code)
			return
		}
		loaded[p.Code] = &p
	}
}

// Get returns a profile by species code.
func Get(code string) (*Profile, error) {
	loadOnce.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	p, ok := loaded[code]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownSpecies, code)
	}
	return p, nil
}

// All returns every known profile, ordered by code so output is stable.
func All() ([]*Profile, error) {
	loadOnce.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	codes := make([]string, 0, len(loaded))
	for c := range loaded {
		codes = append(codes, c)
	}
	sort.Strings(codes)

	out := make([]*Profile, 0, len(codes))
	for _, c := range codes {
		out = append(out, loaded[c])
	}
	return out, nil
}

// Default is the profile used when a caller names none. It exists so the
// existing single-species deployment keeps working through the refactor.
const Default = "CALSAP"
