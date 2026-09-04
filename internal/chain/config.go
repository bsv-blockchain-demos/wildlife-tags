package chain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/defs"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/storage"
)

// Config is everything a deployment needs to know.
//
// Values carry the reasoning that produced them. A constant without its story
// is a constant nobody will dare change later.
type Config struct {
	// Network selects the chain. "test" disables arcade and chaintracks
	// entirely, which is what makes the offline end-to-end tests possible.
	Network defs.BSVNetwork

	// ArcadeURL is the transaction oracle. There is no fallback and no second
	// arcade: if this is wrong, nothing broadcasts.
	ArcadeURL string
	// ChainTracksURL serves block headers. Merkle proofs are verified locally
	// against them, so this is the trust anchor for every timestamp claim the
	// application makes. Defaults to ArcadeURL + "/chaintracks/v2".
	ChainTracksURL string
	// EventsURL is the SSE endpoint; defaults to ArcadeURL.
	EventsURL string

	// DataDir holds keys.json and, for SQLite deployments, both databases.
	DataDir string
	// PostgresDSN, when set, moves both the wallet and the application database
	// into Postgres. Empty means SQLite.
	PostgresDSN string

	// PublicURL is the origin printed into every QR code.
	//
	// Changing this after a batch has been printed bricks those tags: the code
	// on the crab points at the old host forever. It is validated at startup
	// and recorded with each batch for exactly that reason.
	PublicURL string

	// BaseSatoshis is paid to whoever reports a tag.
	BaseSatoshis uint64
	// BonusSatoshis is escrowed when a crab is released with its tag, and paid
	// to that reporter only when the tag is reported again. Nothing on chain
	// can prove a release happened; the next recapture is what corroborates it,
	// which is why the bonus waits for one.
	BonusSatoshis uint64

	// CooldownFor is how long a tag is out of service after a recapture.
	//
	// It exists because the reporter who just walked away still holds the
	// bearer secret. The two-of-two lock is what actually stops them spending
	// the next generation; this is the operational layer on top, giving DNR a
	// window to look at a report before re-arming the tag.
	CooldownFor time.Duration

	// SweepAfter is how long a reward stays claimable before DNR may reclaim it.
	SweepAfter time.Duration

	// FeeSatPerKB funds transactions. 125 rather than the toolbox default 100:
	// the margin protects our own size estimate, not arcade's pricing.
	FeeSatPerKB int64
	// MinBroadcastFeeRate is the floor storage will not broadcast below. Paired
	// with the deliberately generous tagscript.UnlockingScriptEstimate, because
	// an underpaid transaction is rejected with a permanent 4xx that cannot be
	// retried.
	MinBroadcastFeeRate int64

	// MaxDBConns caps the wallet's SQL pool. A redemption that cannot write its
	// pre-signing record must be refused, so exhausting the pool costs money,
	// not just latency.
	MaxDBConns int

	// Originator is the BRC-100 originator string on every wallet call.
	Originator string

	// StorageName names the wallet's storage instance.
	StorageName string
}

// Well-known basket and label names. Baskets are created by being named; there
// is no CreateBasket.
const (
	// TagBasket holds tag outputs. They are custom outputs, so the toolbox
	// marks them unspendable and this application owns their lifecycle -- the
	// tags table is that ledger.
	TagBasket = "wildtags"
	// AppLabel makes every transaction this program creates findable through
	// ListActions, which is what turns "a tag went missing" into a query.
	AppLabel = "wildtag"
)

// DefaultConfig returns a configuration with everything but the URLs filled in.
func DefaultConfig() Config {
	return Config{
		Network:     defs.NetworkTSTN,
		DataDir:     "./data",
		StorageName: "wildtag",
		Originator:  "wildtag.dnr.sc.gov",

		// About a US dollar at the time of writing, split one to three between
		// reporting and releasing. The split is the whole incentive design: a
		// crabber who puts the animal back stands to earn several times what
		// one who keeps it does, and SCDNR wants tagged crabs back in the water
		// because a released crab can produce another data point.
		BaseSatoshis:  5000,
		BonusSatoshis: 15000,

		// A week is long enough that the previous reporter has moved on and
		// short enough that a tag is not out of service for a season.
		CooldownFor: 7 * 24 * time.Hour,

		// Eighteen months. Blue crabs live two to three years and shed a tag at
		// every moult, so a tag still unreported after a year and a half is
		// almost certainly gone.
		SweepAfter: 540 * 24 * time.Hour,

		FeeSatPerKB:         125,
		MinBroadcastFeeRate: 100,
		MaxDBConns:          16,
	}
}

// Validate checks the configuration and fills in derived fields.
//
// It mutates, which is unusual for a Validate, and deliberate: deriving
// ChainTracksURL here means there is exactly one place that knows the
// convention, and a caller that reads its own copy of the config back gets the
// derived values rather than empty strings.
func (c *Config) Validate() error {
	if err := c.Network.Validate(); err != nil {
		return fmt.Errorf("chain: network: %w", err)
	}

	c.ArcadeURL = strings.TrimSuffix(strings.TrimSpace(c.ArcadeURL), "/")
	// The "test" network stands up no services at all, which is what the
	// offline end-to-end tests want; requiring an arcade URL there would mean
	// every test needed a fake one. Whether a network has services is the
	// toolbox's decision, so ask it rather than hardcoding the list here.
	if !c.Offline() && c.ArcadeURL == "" {
		return errors.New("chain: an arcade url is required")
	}

	if c.ChainTracksURL == "" && c.ArcadeURL != "" {
		c.ChainTracksURL = c.ArcadeURL + "/chaintracks/v2"
	}
	if c.EventsURL == "" {
		c.EventsURL = c.ArcadeURL
	}

	c.PublicURL = strings.TrimSuffix(strings.TrimSpace(c.PublicURL), "/")
	if c.PublicURL == "" {
		return errors.New("chain: a public url is required; it is printed into every QR code")
	}
	if !strings.HasPrefix(c.PublicURL, "http://") && !strings.HasPrefix(c.PublicURL, "https://") {
		return fmt.Errorf("chain: public url %q needs a scheme", c.PublicURL)
	}
	// navigator.geolocation refuses to run outside a secure context, and both
	// halves of this application need a position fix. localhost is the one
	// exception browsers make, and it only helps in development.
	if strings.HasPrefix(c.PublicURL, "http://") && !isLoopback(c.PublicURL) {
		return fmt.Errorf("chain: public url %q is plain http; browsers will refuse to give the page a gps fix", c.PublicURL)
	}

	if c.DataDir == "" {
		return errors.New("chain: a data directory is required")
	}
	if c.BaseSatoshis == 0 {
		return errors.New("chain: base reward must be greater than zero")
	}
	if c.CooldownFor < 0 || c.SweepAfter <= 0 {
		return errors.New("chain: cooldown and sweep windows must be positive")
	}
	if c.SweepAfter <= c.CooldownFor {
		return fmt.Errorf("chain: sweep window (%s) must outlast the cooldown (%s), or a tag becomes reclaimable before it can be reported again",
			c.SweepAfter, c.CooldownFor)
	}
	if c.FeeSatPerKB <= 0 {
		return errors.New("chain: fee rate must be greater than zero")
	}
	if c.StorageName == "" {
		c.StorageName = "wildtag"
	}
	if c.Originator == "" {
		c.Originator = "wildtag"
	}
	if c.MaxDBConns <= 0 {
		c.MaxDBConns = 16
	}
	return nil
}

// Offline reports whether this network runs without arcade or chaintracks.
// On an offline network nothing broadcasts and nothing is ever mined, which is
// exactly what the end-to-end tests need.
func (c *Config) Offline() bool {
	return !defs.DefaultServicesConfig(c.Network).Arcade.Enabled
}

// TotalReward is what an activation locks: the report reward plus the escrowed
// re-release bonus, held in one output until somebody finds the crab.
func (c *Config) TotalReward() uint64 { return c.BaseSatoshis + c.BonusSatoshis }

// Mainnet reports whether addresses should be encoded for mainnet.
func (c *Config) Mainnet() bool { return c.Network == defs.NetworkMainnet }

// WalletChain is the network name a BRC-100 client wallet should build itself
// for, which is not always the name this server uses.
//
// The toolbox distinguishes four networks; a wallet knows three plus a mock,
// and calls the Teranode testnets "teratest". A client that guessed would build
// a wallet on the wrong chain -- and the failure is silent until a payment
// arrives and cannot be verified, because the merkle proof is checked against
// headers from a chain the transaction was never on.
//
// So the server publishes it rather than leaving every client to map it. There
// is exactly one place that knows the correspondence, and it is here.
func (c *Config) WalletChain() string {
	switch c.Network {
	case defs.NetworkMainnet:
		return "main"
	case defs.NetworkTTN, defs.NetworkTSTN:
		return "teratest"
	default:
		return "test"
	}
}

// storageOptions are the provider overrides this application needs.
func (c *Config) storageOptions() []storage.Option {
	return []storage.Option{
		storage.WithFeeModel(defs.FeeModel{Type: defs.SatPerKB, Value: c.FeeSatPerKB}),
		storage.WithMinBroadcastFeeRate(c.MinBroadcastFeeRate),

		// A redemption's outputs are fixed before either signature is made, and
		// both signatures commit to all of them. Pinning change to one output
		// keeps the transaction shape predictable, which matters because the
		// payout is at a known index and the browser checks it there.
		storage.WithChangeBasket(defs.ChangeBasket{
			NumberOfDesiredUTXOs:    8,
			MinimumDesiredUTXOValue: 1000,
			MaxChangeOutputsPerTx:   1,
		}),

		// A tag output is spent by a transaction whose parent this wallet does
		// know, but whose ancestry it has no reason to carry. Without this the
		// BEEF handed to storage grows with every generation of every tag.
		storage.WithDirectInputBEEF(),
	}
}

// isLoopback reports whether a URL points at the local machine, which is the
// only origin browsers treat as secure without TLS.
func isLoopback(u string) bool {
	rest := strings.TrimPrefix(u, "http://")
	host, _, _ := strings.Cut(rest, "/")
	host, _, _ = strings.Cut(host, ":")
	return host == "localhost" || host == "127.0.0.1" || host == "[::1]" || host == "::1"
}
