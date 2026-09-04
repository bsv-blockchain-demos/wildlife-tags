package chain

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/wdk"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/defs"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
)

func TestIdentityRoundTrips(t *testing.T) {
	dir := t.TempDir()
	created, err := CreateIdentity(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	loaded, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if *loaded != *created {
		t.Fatal("identity did not survive a round trip")
	}
}

// TestLoadingAMissingIdentityDoesNotMintOne is the guard against a deployment
// silently becoming a different deployment. A wallet that invents a new key
// comes up healthy, reports a balance of zero that is technically correct, and
// abandons every coin the old one held.
func TestLoadingAMissingIdentityDoesNotMintOne(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadIdentity(dir); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("got %v, want ErrNoIdentity", err)
	}
	if _, err := os.Stat(filepath.Join(dir, keysFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed load created a keys file")
	}
}

func TestCreateIdentityRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateIdentity(dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := CreateIdentity(dir); err == nil {
		t.Fatal("a second create overwrote live keys")
	}
}

func TestTheKeysFileIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateIdentity(dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, keysFileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("keys.json is mode %o, want 600", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	// t.TempDir hands back a 0755 directory, so this asserts that creating an
	// identity actively tightens an existing directory rather than trusting
	// MkdirAll -- which applies its mode only when it creates the directory.
	if perm := dirInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("data directory is mode %o, want no group or other access", perm)
	}
}

func TestABrokenIdentityIsRefusedRatherThanPartiallyUsed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, keysFileName), []byte(`{"wallet_key":"zzz"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadIdentity(dir); !errors.Is(err, ErrBrokenIdentity) {
		t.Fatalf("got %v, want ErrBrokenIdentity", err)
	}
}

func TestTheWalletAndCoSigningKeysAreDistinct(t *testing.T) {
	// Keeping them separate means an accident involving one is not
	// automatically an accident involving both.
	dir := t.TempDir()
	id, err := CreateIdentity(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id.WalletKeyHex == id.CoSignKeyHex {
		t.Fatal("the wallet key and the co-signing key are the same key")
	}
}

func TestTagKeysAreRecoverableFromTheSeedAlone(t *testing.T) {
	// This is what makes a lost print sheet survivable and a stranded reward
	// reclaimable.
	dir := t.TempDir()
	id, err := CreateIdentity(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	first, err := id.SecretFor(17)
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	reloaded, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	second, err := reloaded.SecretFor(17)
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if first != second {
		t.Fatal("a tag key could not be recovered from the stored seed")
	}
}

func baseConfig() Config {
	c := DefaultConfig()
	c.Network = defs.NetworkTestnet
	c.PublicURL = "https://bcrab.sc.gov"
	c.DataDir = "/tmp/wildtag-test"
	return c
}

func TestValidateFillsInDerivedURLs(t *testing.T) {
	c := DefaultConfig()
	c.PublicURL = "https://bcrab.sc.gov"
	c.ArcadeURL = "https://arcade.example/"
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.ArcadeURL != "https://arcade.example" {
		t.Errorf("arcade url was not normalised: %q", c.ArcadeURL)
	}
	if c.ChainTracksURL != "https://arcade.example/chaintracks/v2" {
		t.Errorf("chaintracks url was not derived: %q", c.ChainTracksURL)
	}
	if c.EventsURL != "https://arcade.example" {
		t.Errorf("events url was not derived: %q", c.EventsURL)
	}
}

// TestPlainHTTPIsRefusedBecauseGeolocationNeedsASecureContext catches a
// configuration mistake that would otherwise show up as "the GPS button does
// nothing" on a boat, which is the worst possible place to debug it.
func TestPlainHTTPIsRefusedBecauseGeolocationNeedsASecureContext(t *testing.T) {
	c := baseConfig()
	c.PublicURL = "http://wildtag.sc.gov"
	err := c.Validate()
	if err == nil {
		t.Fatal("a plain-http public url was accepted")
	}
	if !strings.Contains(err.Error(), "gps") {
		t.Errorf("the error does not explain the real consequence: %v", err)
	}
}

func TestLocalhostOverPlainHTTPIsAllowed(t *testing.T) {
	// Browsers make exactly one exception to the secure-context rule, and
	// development depends on it.
	for _, u := range []string{"http://localhost:8120", "http://127.0.0.1:8120"} {
		c := baseConfig()
		c.PublicURL = u
		if err := c.Validate(); err != nil {
			t.Errorf("%s was refused: %v", u, err)
		}
	}
}

func TestAMissingPublicURLIsRefused(t *testing.T) {
	c := baseConfig()
	c.PublicURL = ""
	if err := c.Validate(); err == nil {
		t.Fatal("a config with no public url was accepted")
	}
}

// TestTheSweepWindowMustOutlastTheCooldown catches a configuration in which a
// tag becomes reclaimable before it can be reported again -- a reward that
// exists only during a window nobody can reach it in.
func TestTheSweepWindowMustOutlastTheCooldown(t *testing.T) {
	c := baseConfig()
	c.CooldownFor = 30 * 24 * time.Hour
	c.SweepAfter = 7 * 24 * time.Hour
	if err := c.Validate(); err == nil {
		t.Fatal("a sweep window shorter than the cooldown was accepted")
	}
}

func TestTheOfflineNetworkNeedsNoArcade(t *testing.T) {
	c := baseConfig()
	c.ArcadeURL = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("the offline network required an arcade url: %v", err)
	}
	if !c.Offline() {
		t.Fatal("the test network does not report itself as offline")
	}
}

func TestALiveNetworkRequiresAnArcade(t *testing.T) {
	c := baseConfig()
	c.Network = defs.NetworkTSTN
	c.ArcadeURL = ""
	if err := c.Validate(); err == nil {
		t.Fatal("a live network was accepted with no arcade url")
	}
}

func TestTotalRewardIsWhatAnActivationLocks(t *testing.T) {
	c := DefaultConfig()
	if got, want := c.TotalReward(), c.BaseSatoshis+c.BonusSatoshis; got != want {
		t.Fatalf("total reward is %d, want %d", got, want)
	}
}

// TestTheFirstRecaptureEscrowsRatherThanPays is the heart of the incentive
// design. Nothing on chain can prove an animal was released, so the bonus is not
// paid on the reporter's word -- it is locked into the next generation and
// released only when the tag turns up again.
func TestTheFirstRecaptureEscrowsRatherThanPays(t *testing.T) {
	c := &Chain{cfg: DefaultConfig()}
	split, err := c.splitFor(RedeemRequest{
		Attr:         map[string]string{species.DispositionKey: string(species.Released)},
		PrevSatoshis: c.cfg.TotalReward(),
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if split.ReporterSats != c.cfg.BaseSatoshis {
		t.Errorf("reporter paid %d, want the base reward %d", split.ReporterSats, c.cfg.BaseSatoshis)
	}
	if split.EscrowSats != 0 {
		t.Errorf("the first recapture released an escrow of %d; there is nobody to corroborate yet", split.EscrowSats)
	}
	if want := c.cfg.BonusSatoshis + c.cfg.BaseSatoshis; split.NextLockSats != want {
		t.Errorf("re-locked %d, want %d (this reporter's bonus plus the next finder's base)", split.NextLockSats, want)
	}
}

func TestKeepingTheAnimalRetiresTheTagAndForfeitsTheBonus(t *testing.T) {
	c := &Chain{cfg: DefaultConfig()}
	split, err := c.splitFor(RedeemRequest{
		Attr:         map[string]string{species.DispositionKey: string(species.Harvested)},
		PrevSatoshis: c.cfg.TotalReward(),
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if split.ReporterSats != c.cfg.BaseSatoshis {
		t.Errorf("reporter paid %d, want %d", split.ReporterSats, c.cfg.BaseSatoshis)
	}
	if split.NextLockSats != 0 {
		t.Errorf("a kept animal re-locked %d satoshis", split.NextLockSats)
	}
}

// TestASecondRecaptureReleasesTheEscrow is the corroboration step: the tag
// turning up again is the evidence that the previous reporter really did put
// the animal back, so their bonus is paid at the same moment.
func TestASecondRecaptureReleasesTheEscrow(t *testing.T) {
	c := &Chain{cfg: DefaultConfig()}
	locked := c.cfg.BonusSatoshis + c.cfg.BaseSatoshis
	split, err := c.splitFor(RedeemRequest{
		Attr:         map[string]string{species.DispositionKey: string(species.Released)},
		PrevSatoshis: locked,
		PendingEscrow: &PayoutSplit{
			EscrowSats: c.cfg.BonusSatoshis,
			EscrowFor:  "02previousreporter",
		},
	})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if split.EscrowSats != c.cfg.BonusSatoshis {
		t.Errorf("escrow released %d, want %d", split.EscrowSats, c.cfg.BonusSatoshis)
	}
	if split.EscrowFor != "02previousreporter" {
		t.Errorf("escrow beneficiary is %q", split.EscrowFor)
	}
	if split.ReporterSats != c.cfg.BaseSatoshis {
		t.Errorf("this reporter paid %d, want %d", split.ReporterSats, c.cfg.BaseSatoshis)
	}
}

// TestAnUnderfundedTagOutputIsRefused stops the program building a transaction
// that promises more than the output holds. The chain would reject it anyway,
// but only after a crabber had been told they were being paid.
func TestAnUnderfundedTagOutputIsRefused(t *testing.T) {
	c := &Chain{cfg: DefaultConfig()}
	_, err := c.splitFor(RedeemRequest{
		Attr:         map[string]string{species.DispositionKey: string(species.Released)},
		PrevSatoshis: 100,
	})
	if err == nil {
		t.Fatal("a tag output too small to cover the reward was accepted")
	}
}

func TestTheDepositAddressIsStable(t *testing.T) {
	// A deposit address that moves between restarts silently strands whatever
	// was sent to the old one.
	dir := t.TempDir()
	id, err := CreateIdentity(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c := &Chain{cfg: baseConfig(), Identity: id}
	first, err := c.DepositAddress()
	if err != nil {
		t.Fatalf("deposit address: %v", err)
	}

	reloaded, err := LoadIdentity(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	c2 := &Chain{cfg: baseConfig(), Identity: reloaded}
	second, err := c2.DepositAddress()
	if err != nil {
		t.Fatalf("deposit address: %v", err)
	}
	if first != second {
		t.Fatalf("the deposit address moved across a restart: %s then %s", first, second)
	}
}

func TestEveryTagGetsADistinctKey(t *testing.T) {
	dir := t.TempDir()
	id, err := CreateIdentity(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c := &Chain{cfg: baseConfig(), Identity: id}
	seen := map[string]bool{}
	for ord := uint64(0); ord < 500; ord++ {
		pub, err := c.tagPubKey(ord)
		if err != nil {
			t.Fatalf("ordinal %d: %v", ord, err)
		}
		k := pub.ToDERHex()
		if seen[k] {
			t.Fatalf("ordinal %d reused a tag key", ord)
		}
		seen[k] = true
	}
}

// TestTheWalletChainIsPublishedRatherThanGuessed pins the correspondence
// between the network this server runs on and the one a client wallet builds
// itself for.
//
// They are not the same vocabulary. The toolbox has four networks; a BRC-100
// wallet has three plus a mock, and calls both Teranode testnets "teratest". A
// client left to map that itself gets it wrong on the network this project
// actually deploys to -- and the failure is silent until a payment arrives and
// cannot be verified, because the merkle proof is being checked against headers
// from a chain the transaction was never on.
func TestTheWalletChainIsPublishedRatherThanGuessed(t *testing.T) {
	for network, want := range map[defs.BSVNetwork]string{
		defs.NetworkMainnet: "main",
		defs.NetworkTestnet: "test",
		defs.NetworkTTN:     "teratest",
		defs.NetworkTSTN:    "teratest",
	} {
		c := DefaultConfig()
		c.Network = network
		if got := c.WalletChain(); got != want {
			t.Errorf("a %s deployment tells clients to build a %q wallet, want %q", network, got, want)
		}
	}
}

// fakeProofs answers merkle-path lookups from a fixed map.
type fakeProofs struct {
	paths map[string]*transaction.MerklePath
	calls []string
}

func (f *fakeProofs) MerklePath(_ context.Context, txid string) (*wdk.MerklePathResult, error) {
	f.calls = append(f.calls, txid)
	if mp, ok := f.paths[txid]; ok {
		return &wdk.MerklePathResult{MerklePath: mp}, nil
	}
	// What the real service returns for a transaction it knows but that is not
	// mined: a non-error, empty result. Not an error -- that distinction is the
	// whole reason this is best-effort.
	return &wdk.MerklePathResult{}, nil
}

// TestAPaymentsParentCarriesItsProof is the fix for a payment a finder's wallet
// would not accept.
//
// A wallet verifies an incoming payment by walking its BEEF back to a
// transaction with a merkle proof. inputBEEFFor deliberately wraps the parent
// with no proof, which is right for the BEEF handed to storage and wrong for
// the one handed to a finder: theirs has to stand on its own. Without the proof
// the walk reaches nothing proven and the wallet refuses with
// WERR_INVALID_PARAMETER('tx', 'valid AtomicBEEF') -- a message about the
// argument, for a problem that is nothing of the kind. That reached a phone.
func TestAPaymentsParentCarriesItsProof(t *testing.T) {
	parent := transaction.NewTransaction()
	parent.AddOutput(&transaction.TransactionOutput{
		LockingScript: &script.Script{}, Satoshis: 20000,
	})
	txid := parent.TxID().String()

	path := transaction.NewMerklePath(834000, [][]*transaction.PathElement{{
		{Offset: 0, Hash: parent.TxID()},
		{Offset: 1, Hash: parent.TxID()},
	}})

	src := &fakeProofs{paths: map[string]*transaction.MerklePath{txid: path}}
	attachProof(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), src, parent)

	if parent.MerklePath == nil {
		t.Fatal("the parent went out with no proof; a finder's wallet will refuse the payment")
	}
	if parent.MerklePath.BlockHeight != 834000 {
		t.Errorf("attached a proof for height %d", parent.MerklePath.BlockHeight)
	}
	if len(src.calls) != 1 || src.calls[0] != txid {
		t.Errorf("looked up %v, want one lookup for %s", src.calls, txid)
	}
}

// A parent that is not yet mined has no proof to attach, and that is not an
// error: the money has moved and the receipt still names the transaction. The
// client keeps it and internalizes once a block arrives.
func TestAnUnminedParentIsNotAnError(t *testing.T) {
	parent := transaction.NewTransaction()
	parent.AddOutput(&transaction.TransactionOutput{LockingScript: &script.Script{}, Satoshis: 20000})

	src := &fakeProofs{paths: map[string]*transaction.MerklePath{}}
	attachProof(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), src, parent)

	if parent.MerklePath != nil {
		t.Fatal("a proof appeared for a transaction that has none")
	}
}

// A transaction that already has a proof must not be looked up again. Every
// redemption would otherwise pay for a network round trip it does not need.
func TestAnAlreadyProvenParentIsNotLookedUpAgain(t *testing.T) {
	parent := transaction.NewTransaction()
	parent.AddOutput(&transaction.TransactionOutput{LockingScript: &script.Script{}, Satoshis: 20000})
	parent.MerklePath = transaction.NewMerklePath(834000, [][]*transaction.PathElement{{
		{Offset: 0, Hash: parent.TxID()},
	}})

	src := &fakeProofs{paths: map[string]*transaction.MerklePath{}}
	attachProof(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), src, parent)

	if len(src.calls) != 0 {
		t.Errorf("looked up a proof for a transaction that already had one: %v", src.calls)
	}
}

// A deployment with no proof source at all must still complete a redemption.
// The payment is valid regardless; only its receipt is less useful.
func TestNoProofSourceDoesNotBreakARedemption(t *testing.T) {
	parent := transaction.NewTransaction()
	parent.AddOutput(&transaction.TransactionOutput{LockingScript: &script.Script{}, Satoshis: 20000})
	attachProof(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, parent)
	if parent.MerklePath != nil {
		t.Fatal("a proof appeared from nowhere")
	}
}
