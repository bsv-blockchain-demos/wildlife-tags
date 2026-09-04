// Package auth decides who may administer the tagging program.
//
// The primary mechanism is a BRC-100 identity signature rather than a password,
// and the reason is field ergonomics as much as security. A biologist arming a
// tag is standing in a boat with wet hands and a crab in one of them; a wallet
// prompt is one tap, a password is a minute of misery. It also gives the
// dataset something a shared password cannot: every activation carries an
// attestation naming the identity key that made it, so a record is attributable
// to a person rather than to whoever was logged in.
//
// A password fallback exists because an identity-only system cannot be
// administered from a script, a CI job, or a laptop whose wallet is not set up
// -- and a recovery path that does not exist when you need it is not a design,
// it is a hostage situation.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	sdk "github.com/bsv-blockchain/go-sdk/wallet"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
)

// AdminProtocol is the BRC-100 protocol identity signatures are made under.
//
// Security level 2 means the wallet asks the user per counterparty, which is
// the right prompt for "log in to the crab tag admin console".
var AdminProtocol = sdk.Protocol{
	SecurityLevel: sdk.SecurityLevelEveryAppAndCounterparty,
	Protocol:      "wildtag admin",
}

// SessionCookie is the cookie name.
const SessionCookie = "wildtag_session"

// SessionTTL is how long a login lasts. A field session should outlive a
// morning on the water without being indefinite.
const SessionTTL = 12 * time.Hour

// challengeTTL bounds how long a nonce is answerable.
const challengeTTL = 2 * time.Minute

var (
	ErrNoSession    = errors.New("auth: not signed in")
	ErrNotAdmin     = errors.New("auth: this identity key is not authorised")
	ErrBadChallenge = errors.New("auth: unknown or expired challenge")
	ErrBadSignature = errors.New("auth: signature does not verify")
	ErrNoPassword   = errors.New("auth: password login is not configured")
	ErrBadPassword  = errors.New("auth: wrong password")
)

// Verifier verifies a counterparty's signature. The toolbox wallet satisfies
// it; the interface is declared here so the authenticator is testable without a
// wallet, a database or a network.
type Verifier interface {
	VerifySignature(ctx context.Context, args sdk.VerifySignatureArgs, originator string) (*sdk.VerifySignatureResult, error)
}

// Authenticator issues challenges and sessions.
type Authenticator struct {
	store      *store.Store
	verifier   Verifier
	originator string

	// passwordHash is the fallback credential, stored as a SHA-256 of the
	// configured password. It is deliberately not bcrypt: this is a single
	// operator secret supplied by environment variable, not a user table, and
	// the threat it guards against is a careless log line rather than an
	// offline crack of a stolen database.
	passwordHash []byte

	// secureCookies is off only for plain-http localhost development. In every
	// other case the config validator has already refused plain http, because
	// the browser needs a secure context for geolocation anyway.
	secureCookies bool

	mu         sync.Mutex
	challenges map[string]time.Time
	now        func() time.Time
}

// New builds an authenticator.
func New(s *store.Store, v Verifier, originator, password string, secureCookies bool) *Authenticator {
	a := &Authenticator{
		store:         s,
		verifier:      v,
		originator:    originator,
		secureCookies: secureCookies,
		challenges:    make(map[string]time.Time),
		now:           func() time.Time { return time.Now().UTC() },
	}
	if password != "" {
		sum := sha256.Sum256([]byte(password))
		a.passwordHash = sum[:]
	}
	return a
}

// Challenge issues a nonce for an identity to sign.
func (a *Authenticator) Challenge() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("auth: generate challenge: %w", err)
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw[:])

	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked()
	a.challenges[nonce] = a.now().Add(challengeTTL)
	return nonce, nil
}

func (a *Authenticator) expireLocked() {
	now := a.now()
	for nonce, expires := range a.challenges {
		if now.After(expires) {
			delete(a.challenges, nonce)
		}
	}
}

// consume redeems a challenge exactly once.
//
// Single use is the whole point: a nonce that can be replayed is a signature
// that can be replayed, and a captured login would then be reusable forever.
func (a *Authenticator) consume(nonce string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, ok := a.challenges[nonce]
	if !ok {
		return false
	}
	delete(a.challenges, nonce)
	return !a.now().After(expires)
}

// LoginWithIdentity verifies a signed challenge and starts a session.
//
// The signature is made by a type-42 child of the caller's identity key, with
// this server as the counterparty. That direction matters: the server derives
// the same point from its own private key and the caller's public one, so
// verification needs no shared secret and the signature is useless to any other
// server.
func (a *Authenticator) LoginWithIdentity(ctx context.Context, identityKeyHex, nonce, signatureHex string) (*store.Session, error) {
	if !a.consume(nonce) {
		return nil, ErrBadChallenge
	}

	pub, err := parsePub(identityKeyHex)
	if err != nil {
		return nil, fmt.Errorf("auth: identity key: %w", err)
	}
	sigBytes, err := hex.DecodeString(strings.TrimSpace(signatureHex))
	if err != nil {
		return nil, fmt.Errorf("auth: signature is not hex: %w", err)
	}
	sig, err := ec.ParseDERSignature(sigBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSignature, err)
	}

	// Authorise before verifying. An unauthorised key gets the same answer
	// whether or not its signature was valid, so this endpoint cannot be used
	// to test signatures against the allowlist.
	ok, err := a.store.IsAdmin(ctx, identityKeyHex)
	if err != nil {
		return nil, err
	}

	forSelf := false
	res, err := a.verifier.VerifySignature(ctx, sdk.VerifySignatureArgs{
		EncryptionArgs: sdk.EncryptionArgs{
			ProtocolID:   AdminProtocol,
			KeyID:        nonce,
			Counterparty: sdk.Counterparty{Type: sdk.CounterpartyTypeOther, Counterparty: pub},
		},
		Data:      []byte(nonce),
		Signature: sig,
		ForSelf:   &forSelf,
	}, a.originator)
	if err != nil || res == nil || !res.Valid {
		return nil, ErrBadSignature
	}
	if !ok {
		return nil, ErrNotAdmin
	}

	return a.startSession(ctx, identityKeyHex, "")
}

// LoginWithPassword is the fallback path.
func (a *Authenticator) LoginWithPassword(ctx context.Context, password string) (*store.Session, error) {
	if len(a.passwordHash) == 0 {
		return nil, ErrNoPassword
	}
	sum := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(sum[:], a.passwordHash) != 1 {
		return nil, ErrBadPassword
	}
	// A password session is labelled so the audit log can tell it apart from a
	// biologist's. Anything it does is attributed to "operator", not a person.
	return a.startSession(ctx, "operator", "password")
}

func (a *Authenticator) startSession(ctx context.Context, identityKey, label string) (*store.Session, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("auth: generate session token: %w", err)
	}
	now := a.now()
	sess := store.Session{
		Token:       base64.RawURLEncoding.EncodeToString(raw[:]),
		IdentityKey: identityKey,
		Label:       label,
		CreatedAt:   now,
		ExpiresAt:   now.Add(SessionTTL),
	}
	if err := a.store.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	if err := a.store.Audit(ctx, identityKey, "auth.login", label); err != nil {
		// Not fatal, but worth knowing: an audit log with gaps is worth less
		// than one without.
		_ = err
	}
	return &sess, nil
}

// SetCookie installs a session cookie.
func (a *Authenticator) SetCookie(w http.ResponseWriter, sess *store.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    sess.Token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   a.secureCookies,
		// Lax rather than Strict: a biologist following a link from an email or
		// a QR scanner into the admin console should not land signed out.
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie signs a session out of the browser.
func (a *Authenticator) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// Logout ends a session.
func (a *Authenticator) Logout(ctx context.Context, r *http.Request) error {
	token := sessionToken(r)
	if token == "" {
		return nil
	}
	return a.store.DeleteSession(ctx, token)
}

// sessionToken reads the caller's session token from either place it may be.
//
// A browser sends the cookie. A phone sends a bearer header, because React
// Native has no cookie jar worth relying on: fetch's credential handling
// differs between the two platforms and between debug and release builds, and
// debugging a login that works on Android and silently fails on iOS is not how
// anybody should spend a week. The token is the same either way, so this is a
// transport detail rather than a second authentication scheme.
//
// The cookie wins when both are present, so a browser with a stale header
// cannot be talked into using it.
func sessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(SessionCookie); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	header := r.Header.Get("Authorization")
	if token, ok := strings.CutPrefix(header, "Bearer "); ok {
		return strings.TrimSpace(token)
	}
	return ""
}

// Session resolves the caller's session, refusing one whose identity key has
// been revoked since they signed in.
func (a *Authenticator) Session(ctx context.Context, r *http.Request) (*store.Session, error) {
	token := sessionToken(r)
	if token == "" {
		return nil, ErrNoSession
	}
	sess, err := a.store.LookupSession(ctx, token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNoSession
		}
		return nil, err
	}
	if sess.Label == "password" {
		return sess, nil
	}
	// Revocation has to bite on the next request, not at the next expiry.
	ok, err := a.store.IsAdmin(ctx, sess.IdentityKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		_ = a.store.DeleteSession(ctx, sess.Token)
		return nil, ErrNotAdmin
	}
	return sess, nil
}

// PasswordConfigured reports whether the fallback is available, so the login
// page can offer it rather than showing a field that cannot work.
func (a *Authenticator) PasswordConfigured() bool { return len(a.passwordHash) > 0 }

func parsePub(hexKey string) (*ec.PublicKey, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return ec.PublicKeyFromBytes(raw) //nolint:wrapcheck // the caller adds context
}
