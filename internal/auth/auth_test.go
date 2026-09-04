package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
)

// fakeVerifier lets the tests control whether a signature verifies without
// standing up a wallet. What is being tested here is the authorisation logic
// around the signature, not secp256k1.
type fakeVerifier struct {
	valid bool
	calls int
}

func (f *fakeVerifier) VerifySignature(_ context.Context, _ sdk.VerifySignatureArgs, _ string) (*sdk.VerifySignatureResult, error) {
	f.calls++
	return &sdk.VerifySignatureResult{Valid: f.valid}, nil
}

func newAuth(t *testing.T, valid bool, password string) (*Authenticator, *store.Store, *fakeVerifier) {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tags.db"), "")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	v := &fakeVerifier{valid: valid}
	return New(s, v, "wildtag", password, true), s, v
}

func adminKey(t *testing.T) (*ec.PrivateKey, string) {
	t.Helper()
	k, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return k, k.PubKey().ToDERHex()
}

func TestAnAuthorisedIdentityCanSignIn(t *testing.T) {
	a, s, _ := newAuth(t, true, "")
	_, keyHex := adminKey(t)
	if err := s.AddAdmin(t.Context(), keyHex, "Graham"); err != nil {
		t.Fatalf("add admin: %v", err)
	}

	nonce, err := a.Challenge()
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	sess, err := a.LoginWithIdentity(t.Context(), keyHex, nonce, validDERHex)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if sess.IdentityKey != keyHex {
		t.Fatalf("session names %s", sess.IdentityKey)
	}
}

func TestAnUnauthorisedIdentityIsRefused(t *testing.T) {
	a, _, _ := newAuth(t, true, "")
	_, keyHex := adminKey(t)

	nonce, _ := a.Challenge()
	if _, err := a.LoginWithIdentity(t.Context(), keyHex, nonce, validDERHex); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("got %v, want ErrNotAdmin", err)
	}
}

// TestTheSignatureIsCheckedEvenForAnUnauthorisedKey stops the login endpoint
// being usable as an oracle for the allowlist. Both paths do the same work, so
// the answer takes the same shape either way.
func TestTheSignatureIsCheckedEvenForAnUnauthorisedKey(t *testing.T) {
	a, _, v := newAuth(t, true, "")
	_, keyHex := adminKey(t)

	nonce, _ := a.Challenge()
	_, _ = a.LoginWithIdentity(t.Context(), keyHex, nonce, validDERHex)
	if v.calls != 1 {
		t.Fatalf("the signature was verified %d times for an unauthorised key; the endpoint short-circuits", v.calls)
	}
}

func TestABadSignatureIsRefused(t *testing.T) {
	a, s, _ := newAuth(t, false, "")
	_, keyHex := adminKey(t)
	_ = s.AddAdmin(t.Context(), keyHex, "Graham")

	nonce, _ := a.Challenge()
	if _, err := a.LoginWithIdentity(t.Context(), keyHex, nonce, validDERHex); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("got %v, want ErrBadSignature", err)
	}
}

// TestAChallengeIsSingleUse is the property that stops a captured login being
// replayed forever.
func TestAChallengeIsSingleUse(t *testing.T) {
	a, s, _ := newAuth(t, true, "")
	_, keyHex := adminKey(t)
	_ = s.AddAdmin(t.Context(), keyHex, "Graham")

	nonce, _ := a.Challenge()
	if _, err := a.LoginWithIdentity(t.Context(), keyHex, nonce, validDERHex); err != nil {
		t.Fatalf("first login: %v", err)
	}
	if _, err := a.LoginWithIdentity(t.Context(), keyHex, nonce, validDERHex); !errors.Is(err, ErrBadChallenge) {
		t.Fatalf("a nonce was accepted twice: %v", err)
	}
}

func TestAnExpiredChallengeIsRefused(t *testing.T) {
	a, s, _ := newAuth(t, true, "")
	_, keyHex := adminKey(t)
	_ = s.AddAdmin(t.Context(), keyHex, "Graham")

	nonce, _ := a.Challenge()
	a.now = func() time.Time { return time.Now().UTC().Add(challengeTTL + time.Minute) }
	if _, err := a.LoginWithIdentity(t.Context(), keyHex, nonce, validDERHex); !errors.Is(err, ErrBadChallenge) {
		t.Fatalf("got %v, want ErrBadChallenge", err)
	}
}

func TestAnUnknownChallengeIsRefused(t *testing.T) {
	a, _, _ := newAuth(t, true, "")
	_, keyHex := adminKey(t)
	if _, err := a.LoginWithIdentity(t.Context(), keyHex, "never-issued", validDERHex); !errors.Is(err, ErrBadChallenge) {
		t.Fatalf("got %v, want ErrBadChallenge", err)
	}
}

func TestThePasswordFallback(t *testing.T) {
	a, _, _ := newAuth(t, true, "correct horse")
	if !a.PasswordConfigured() {
		t.Fatal("the password fallback reports itself unconfigured")
	}
	sess, err := a.LoginWithPassword(t.Context(), "correct horse")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	// A password session is labelled, so anything it does is attributed to an
	// operator rather than to a named biologist.
	if sess.IdentityKey != "operator" || sess.Label != "password" {
		t.Fatalf("session is %+v", sess)
	}
	if _, err := a.LoginWithPassword(t.Context(), "wrong"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("got %v, want ErrBadPassword", err)
	}
}

func TestThePasswordFallbackIsOffWhenUnset(t *testing.T) {
	a, _, _ := newAuth(t, true, "")
	if a.PasswordConfigured() {
		t.Fatal("an unset password reports as configured")
	}
	if _, err := a.LoginWithPassword(t.Context(), ""); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("got %v, want ErrNoPassword", err)
	}
}

// TestRevocationBitesOnTheNextRequest: a revoked administrator must lose access
// immediately, not when their session happens to expire.
func TestRevocationBitesOnTheNextRequest(t *testing.T) {
	a, s, _ := newAuth(t, true, "")
	_, keyHex := adminKey(t)
	_ = s.AddAdmin(t.Context(), keyHex, "Graham")

	nonce, _ := a.Challenge()
	sess, err := a.LoginWithIdentity(t.Context(), keyHex, nonce, validDERHex)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: sess.Token})
	if _, err := a.Session(t.Context(), req); err != nil {
		t.Fatalf("session should be live: %v", err)
	}

	if err := s.RemoveAdmin(t.Context(), keyHex); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := a.Session(t.Context(), req); err == nil {
		t.Fatal("a revoked administrator still has access")
	}
}

func TestNoCookieMeansNoSession(t *testing.T) {
	a, _, _ := newAuth(t, true, "")
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	if _, err := a.Session(t.Context(), req); !errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v, want ErrNoSession", err)
	}
}

func TestTheSessionCookieIsHardened(t *testing.T) {
	a, _, _ := newAuth(t, true, "pw")
	sess, err := a.LoginWithPassword(t.Context(), "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	rec := httptest.NewRecorder()
	a.SetCookie(rec, sess)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies", len(cookies))
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("the session cookie is readable from JavaScript")
	}
	if !c.Secure {
		t.Error("the session cookie is not marked Secure")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite is %v, want Lax", c.SameSite)
	}
}

func TestChallengesAreUnique(t *testing.T) {
	a, _, _ := newAuth(t, true, "")
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		n, err := a.Challenge()
		if err != nil {
			t.Fatalf("challenge: %v", err)
		}
		if seen[n] {
			t.Fatal("a nonce repeated")
		}
		seen[n] = true
	}
}

// validDERHex is a well-formed DER signature. Whether it verifies is decided by
// the fake verifier; this only has to get past parsing.
const validDERHex = "3044" +
	"0220" + "1111111111111111111111111111111111111111111111111111111111111111" +
	"0220" + "2222222222222222222222222222222222222222222222222222222222222222"
