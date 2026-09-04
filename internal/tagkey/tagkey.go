// Package tagkey derives everything a physical crab tag carries.
//
// One master seed produces every tag. From an ordinal (the tag's position in
// the program, which DNR keeps in its database) comes a 128-bit bearer secret;
// from that secret comes both the tag's spending key and its printed tag ID:
//
//	secret = HMAC-SHA256(seed, "wildtag-v1|" + ordinal)[:16]
//	tagKey = SHA256("wildtag-v1-key|" + secret)
//	tagID  = crockford32(SHA256("wildtag-v1-id|" + secret)[:4]) + check
//
// The chain runs one way only, which is what makes the QR self-contained: a
// crabber's browser reads the secret out of the URL fragment and derives both
// the key and the ID without asking the server for anything. DNR walks the same
// chain from the seed, which is what makes a lost print sheet recoverable and
// lets rewards on tags that are never recaptured be swept rather than stranded.
// Crabs die and tags are shed at every molt, so a meaningful fraction of tags
// never come back; without derived keys those rewards would be locked forever.
//
// The cost of that recoverability is real and worth stating: whoever holds the
// seed can spend any tag. It belongs in keys.json at 0600 and nowhere else.
package tagkey

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
)

// crockford is Douglas Crockford's base32 alphabet: no I, L, O or U, so there
// is nothing to confuse with 1, 0, or a word nobody wants printed on a
// government tag. Tag IDs get read aloud over the phone when a QR is too
// fouled to scan, which is the whole reason for the unambiguous alphabet.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// idDataLen is how many alphabet characters carry data; one check character
// follows. 6 characters is 30 bits, which at the scale of a state tagging
// program (thousands of tags) leaves collisions vanishingly unlikely, and
// uniqueness is enforced by the database anyway.
const idDataLen = 6

// IDLen is the length of a canonical tag ID: data characters plus check.
const IDLen = idDataLen + 1

// SecretLen is the bearer secret's size in bytes. 128 bits is well past what
// anyone will brute-force, and every byte costs QR density on a tag the size of
// a postage stamp.
const SecretLen = 16

var (
	ErrBadSecret  = errors.New("tagkey: malformed tag secret")
	ErrBadID      = errors.New("tagkey: malformed tag id")
	ErrBadCheck   = errors.New("tagkey: tag id check character does not match")
	ErrShortSeed  = errors.New("tagkey: seed must be 32 bytes")
	ErrBadPayload = errors.New("tagkey: malformed qr payload")
)

// Seed is the master secret every tag descends from.
type Seed [32]byte

// Secret is the 128-bit bearer value printed into a tag's QR code.
type Secret [SecretLen]byte

// ID is a canonical tag ID: IDLen uppercase Crockford base32 characters, the
// last of which is a check character.
type ID string

// NewSeed draws a fresh master seed.
func NewSeed() (Seed, error) {
	var s Seed
	if _, err := rand.Read(s[:]); err != nil {
		return Seed{}, fmt.Errorf("tagkey: read random seed: %w", err)
	}
	return s, nil
}

// SeedFromBytes adopts an existing seed.
func SeedFromBytes(b []byte) (Seed, error) {
	if len(b) != 32 {
		return Seed{}, ErrShortSeed
	}
	var s Seed
	copy(s[:], b)
	return s, nil
}

// SecretFor derives the bearer secret for a tag's ordinal.
func (s Seed) SecretFor(ordinal uint64) Secret {
	mac := hmac.New(sha256.New, s[:])
	// Writing to a hash never fails, and errcheck noise here would obscure the
	// derivation, which is the only thing in this function worth reading.
	_, _ = mac.Write([]byte("wildtag-v1|"))
	_, _ = mac.Write([]byte(strconv.FormatUint(ordinal, 10)))
	sum := mac.Sum(nil)

	var sec Secret
	copy(sec[:], sum[:SecretLen])
	return sec
}

// PrivateKey is the tag's half of the two-of-two lock. The crabber's browser
// derives this from the QR fragment; it is the proof of physical possession.
func (sec Secret) PrivateKey() *ec.PrivateKey {
	h := sha256.New()
	_, _ = h.Write([]byte("wildtag-v1-key|"))
	_, _ = h.Write(sec[:])
	priv, _ := ec.PrivateKeyFromBytes(h.Sum(nil))
	return priv
}

// ID is the tag's printed identifier, derived from the secret so that a
// scanning browser can name the tag it is holding without a round trip.
func (sec Secret) ID() ID {
	h := sha256.New()
	_, _ = h.Write([]byte("wildtag-v1-id|"))
	_, _ = h.Write(sec[:])
	sum := h.Sum(nil)

	n := binary.BigEndian.Uint32(sum[:4])
	buf := make([]byte, idDataLen)
	for i := idDataLen - 1; i >= 0; i-- {
		buf[i] = crockford[n&31]
		n >>= 5
	}
	return ID(append(buf, checkChar(buf)))
}

// Encode renders the secret for the QR fragment: unpadded base64url, 22
// characters for 16 bytes. Padding is dropped because '=' buys nothing in a
// fixed-length field and costs a QR character.
func (sec Secret) Encode() string {
	return base64.RawURLEncoding.EncodeToString(sec[:])
}

// DecodeSecret parses the fragment form.
func DecodeSecret(s string) (Secret, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return Secret{}, fmt.Errorf("%w: %v", ErrBadSecret, err)
	}
	if len(b) != SecretLen {
		return Secret{}, fmt.Errorf("%w: got %d bytes, want %d", ErrBadSecret, len(b), SecretLen)
	}
	var sec Secret
	copy(sec[:], b)
	return sec, nil
}

// ParseID normalises anything a human might type or read aloud into a canonical
// tag ID, then verifies the check character.
//
// Lowercase is accepted, dashes and spaces are dropped, and the three
// substitutions Crockford specifies are applied: I and L read as 1, O reads
// as 0. Somebody squinting at a barnacled tag on a rocking boat will make
// exactly those mistakes.
func ParseID(s string) (ID, error) {
	var b []byte
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		switch r {
		case '-', ' ', '_':
			continue
		case 'I', 'L':
			r = '1'
		case 'O':
			r = '0'
		}
		if !strings.ContainsRune(crockford, r) {
			return "", fmt.Errorf("%w: %q is not a tag id character", ErrBadID, r)
		}
		b = append(b, byte(r))
	}
	if len(b) != IDLen {
		return "", fmt.Errorf("%w: got %d characters, want %d", ErrBadID, len(b), IDLen)
	}
	if b[idDataLen] != checkChar(b[:idDataLen]) {
		return "", ErrBadCheck
	}
	return ID(b), nil
}

// Display groups a tag ID for reading aloud and for printing on the tag.
func (id ID) Display() string {
	if len(id) != IDLen {
		return string(id)
	}
	return string(id[:3]) + "-" + string(id[3:])
}

// checkChar is a position-weighted sum over the alphabet, using odd weights.
//
// The weights are odd on purpose, and the choice is forced. A single wrong
// character at position i shifts the sum by w_i*delta, which is invisible
// exactly when w_i*delta is a multiple of 32 -- so every weight must be coprime
// to 32, which for a power of two means odd. An adjacent transposition shifts
// the sum by (w_i - w_i+1)*(v_i - v_i+1), and the difference of two odd numbers
// is always even, so some transpositions are always invisible.
//
// One base-32 check character cannot catch both classes completely: even
// weights catch every transposition but miss 5 single-character errors, odd
// weights catch every single-character error but miss 3.2% of adjacent
// transpositions. Getting both would take a Damm quasigroup and a 1KB table.
//
// Odd wins because a misread character is far more common than a swap when
// somebody is squinting at a barnacled tag, and because the database is the
// real backstop: a corrupted id that slips past the check almost certainly
// names a tag that does not exist. The check character is a transcription aid,
// not an authority. Both rates are measured by the tests.
func checkChar(data []byte) byte {
	sum := 0
	for i, c := range data {
		sum += (2*i + 1) * strings.IndexByte(crockford, c)
	}
	return crockford[sum%32]
}

// QRPayload is the exact string encoded into a tag's QR code.
//
// The secret rides in the fragment, which browsers never transmit. That keeps
// it out of server access logs, out of Referer headers, and out of any
// analytics in front of the app -- and since redemption signs in the browser,
// the server never needs it at all. Putting it in the path instead would hand
// every tag secret to whoever can read the web logs.
func QRPayload(publicURL string, id ID, sec Secret) string {
	return strings.TrimSuffix(publicURL, "/") + "/t/" + string(id) + "#" + sec.Encode()
}

// ParsePayload is the inverse of QRPayload, for tests and for the CLI.
func ParsePayload(payload string) (ID, Secret, error) {
	hash := strings.LastIndex(payload, "#")
	if hash < 0 {
		return "", Secret{}, fmt.Errorf("%w: no fragment", ErrBadPayload)
	}
	sec, err := DecodeSecret(payload[hash+1:])
	if err != nil {
		return "", Secret{}, err
	}
	path := payload[:hash]
	slash := strings.LastIndex(path, "/")
	if slash < 0 {
		return "", Secret{}, fmt.Errorf("%w: no tag id", ErrBadPayload)
	}
	id, err := ParseID(path[slash+1:])
	if err != nil {
		return "", Secret{}, err
	}
	return id, sec, nil
}
