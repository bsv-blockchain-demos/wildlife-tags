package chain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/arcade"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/defs"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/headers"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/monitor"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/services"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/storage"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/storage/perfprovider"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/wallet"
	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/wdk"
	"github.com/bsv-blockchain/go-sdk/chainhash"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
)

// Chain is the wallet, its storage, and the background machinery that keeps
// their view of the network current.
type Chain struct {
	cfg      Config
	Identity *Identity
	Wallet   *wallet.Wallet
	Storage  *storage.Provider
	Services *services.Services

	logger    *slog.Logger
	oracle    arcade.TxOracle
	coSignKey *ec.PrivateKey
	monitor   *monitor.Daemon
	closeStor perfprovider.CloseFunc

	// observers receive arcade status updates. They exist so the web layer can
	// follow transactions without opening a second SSE subscription: arcade's
	// /events has no per-client filter, so a second connection on the same
	// callback token receives a full duplicate of every event and doubles
	// arcade's fan-out cost for nothing.
	obsMu     sync.RWMutex
	observers []func([]arcade.TxRecord)

	closeOnce sync.Once
}

// Open wires the toolbox.
//
// The order is fixed by the library: one Services per process, one storage
// Provider per wallet, wallet, then monitor. The monitor is not optional --
// without it nothing is ever promoted past "broadcast", so no tag ever shows a
// merkle proof and the program's central claim about timestamps is unbacked.
func Open(ctx context.Context, logger *slog.Logger, cfg Config, id *Identity) (*Chain, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if id == nil {
		return nil, ErrNoIdentity
	}

	walletKey, err := id.WalletKey()
	if err != nil {
		return nil, fmt.Errorf("chain: wallet key: %w", err)
	}
	coSignKey, err := id.CoSignKey()
	if err != nil {
		return nil, fmt.Errorf("chain: co-signing key: %w", err)
	}
	identityKeyHex, err := id.WalletIdentityKeyHex()
	if err != nil {
		return nil, err
	}

	// Services configuration. The callback token is NOT wired automatically,
	// and skipping it is not a subtle failure: an untokened SSE client is
	// treated as a slow one, events are dropped by the tens of thousands, and
	// transactions simply never receive a status.
	svcCfg := defs.DefaultServicesConfig(cfg.Network)
	if cfg.ArcadeURL != "" {
		svcCfg.Arcade.Enabled = true
		svcCfg.Arcade.URL = cfg.ArcadeURL
		svcCfg.Arcade.EventsURL = cfg.EventsURL
	}
	if cfg.ChainTracksURL != "" && svcCfg.ChainTracks.Enabled {
		svcCfg.ChainTracks.URL = cfg.ChainTracksURL
	}
	if svcCfg.Arcade.CallbackToken == "" {
		svcCfg.Arcade.CallbackToken = wdk.DeriveArcadeCallbackToken(identityKeyHex)
	}
	if err := svcCfg.Validate(); err != nil {
		return nil, fmt.Errorf("chain: services config: %w", err)
	}

	oracle := arcade.New(logger, nil, svcCfg.Arcade)

	// headers.New refuses an empty URL, so a network with no header service
	// needs an explicit stand-in rather than a nil interface.
	var (
		hdrs headers.Headers
		sub  headers.ChainSubscriber
	)
	if svcCfg.ChainTracks.Enabled && svcCfg.ChainTracks.URL != "" {
		// An unbounded header cache: a batch of tags activated in one morning
		// lands in a handful of blocks, and their proofs then share a single
		// header fetch instead of one apiece.
		client, herr := headers.New(logger, svcCfg.ChainTracks, headers.WithCacheDepth(0))
		if herr != nil {
			return nil, fmt.Errorf("chain: chaintracks: %w", herr)
		}
		hdrs, sub = client, client
	} else {
		hdrs = disabledHeaders{}
	}

	svc := services.New(logger, oracle, hdrs, svcCfg)

	pcfg := perfprovider.Config{
		Backend:      perfprovider.BackendSQLite,
		SQLitePath:   filepath.Join(cfg.DataDir, "wallet.db"),
		Network:      cfg.Network,
		StorageName:  cfg.StorageName,
		MaxDBConns:   cfg.MaxDBConns,
		FeeModel:     defs.FeeModel{Type: defs.SatPerKB, Value: cfg.FeeSatPerKB},
		ExtraOptions: cfg.storageOptions(),
	}
	if cfg.PostgresDSN != "" {
		pcfg.Backend = perfprovider.BackendPostgres
		pcfg.PostgresDSN = cfg.PostgresDSN
	}

	provider, closeProvider, err := perfprovider.New(ctx, logger, pcfg, oracle, hdrs)
	if err != nil {
		return nil, fmt.Errorf("chain: storage provider: %w", err)
	}

	c := &Chain{
		cfg:       cfg,
		Identity:  id,
		Storage:   provider,
		Services:  svc,
		logger:    logger,
		oracle:    oracle,
		coSignKey: coSignKey,
		closeStor: closeProvider,
	}

	if _, err := provider.Migrate(ctx, cfg.StorageName, identityKeyHex); err != nil {
		_ = c.Close(ctx)
		return nil, fmt.Errorf("chain: migrate wallet storage: %w", err)
	}

	w, err := wallet.New(cfg.Network, walletKey, provider,
		wallet.WithServices(svc),
		wallet.WithLogger(logger),
	)
	if err != nil {
		_ = c.Close(ctx)
		return nil, fmt.Errorf("chain: wallet: %w", err)
	}
	c.Wallet = w

	// The monitor is mandatory on any network that has an arcade: without it
	// nothing is ever promoted past "broadcast", so no tag ever shows a merkle
	// proof and the program's central claim about timestamps goes unbacked.
	//
	// On an offline network it is not merely unnecessary but actively wrong.
	// Its whole job is applying status that arcade reports, and with no arcade
	// its event consumer sits in a reconnect loop against an empty URL,
	// producing a stream of warnings that make a working offline run look
	// broken.
	if !cfg.Offline() {
		// A single-instance deployment does not need the distributed lease, and
		// taking one means a restart waits for the previous holder's lease to
		// expire before any status is applied.
		monCfg := defs.DefaultMonitorConfig()
		daemon, err := monitor.NewDaemon(logger, provider, sub, oracle, monCfg,
			monitor.WithoutDistributedLock(),
			monitor.WithStatusObserver(c.dispatchStatus),
		)
		if err != nil {
			_ = c.Close(ctx)
			return nil, fmt.Errorf("chain: monitor: %w", err)
		}
		if err := daemon.Start(ctx, monCfg.Tasks.EnabledTasks()); err != nil {
			_ = c.Close(ctx)
			return nil, fmt.Errorf("chain: start monitor: %w", err)
		}
		c.monitor = daemon
	} else {
		logger.Info("offline network: no arcade, no monitor, nothing will be broadcast or proven",
			"network", string(cfg.Network))
	}

	return c, nil
}

// Close shuts everything down in reverse order of construction.
func (c *Chain) Close(ctx context.Context) error {
	var err error
	c.closeOnce.Do(func() {
		if c.monitor != nil {
			c.monitor.Stop()
		}
		if c.Wallet != nil {
			c.Wallet.Close()
		}
		if c.closeStor != nil {
			if cerr := c.closeStor(ctx); cerr != nil {
				err = errors.Join(err, fmt.Errorf("chain: close storage: %w", cerr))
			}
		}
	})
	return err
}

// OnStatus registers an observer for arcade status updates.
//
// The contract is the monitor's: the callback runs inline on the applier
// goroutine, so it must not block and must not panic; delivery is at-least-once,
// so it must be idempotent; and the slice must not be retained or mutated.
func (c *Chain) OnStatus(fn func([]arcade.TxRecord)) {
	c.obsMu.Lock()
	defer c.obsMu.Unlock()
	c.observers = append(c.observers, fn)
}

func (c *Chain) dispatchStatus(records []arcade.TxRecord) {
	c.obsMu.RLock()
	observers := c.observers
	c.obsMu.RUnlock()
	for _, fn := range observers {
		fn(records)
	}
}

// CoSignKey is DNR's half of the two-of-two on every tag output.
func (c *Chain) CoSignKey() *ec.PrivateKey { return c.coSignKey }

// CoSignPubKey is the public half, which appears in every tag locking script.
func (c *Chain) CoSignPubKey() *ec.PublicKey { return c.coSignKey.PubKey() }

// Balance reports the wallet's spendable satoshis.
func (c *Chain) Balance(ctx context.Context) (uint64, error) {
	bal, err := c.Wallet.Balance(ctx)
	if err != nil {
		return 0, fmt.Errorf("chain: balance: %w", err)
	}
	return bal, nil
}

// disabledHeaders stands in for a header service on networks that have none.
//
// It exists because headers.New refuses an empty URL and services.New requires
// a non-nil Headers. Every method reports that nothing is known, which is the
// truth on an offline network -- and means a merkle proof is never claimed to
// verify when there is nothing to verify it against.
type disabledHeaders struct{}

var errHeadersDisabled = errors.New("chain: header service is disabled on this network")

func (disabledHeaders) CurrentHeight(context.Context) (uint32, error) {
	return 0, errHeadersDisabled
}

func (disabledHeaders) HeaderByHeight(context.Context, uint32) (*headers.Header, error) {
	return nil, errHeadersDisabled
}

func (disabledHeaders) VerifyMerkleRoot(context.Context, *chainhash.Hash, uint32) (bool, error) {
	return false, errHeadersDisabled
}

// Config returns the running configuration.
//
// A method rather than an exported field, so *Chain satisfies the narrow
// Ledger interface the service layer declares for it -- which is what lets the
// escrow lifecycle be driven end to end against a fake.
func (c *Chain) Config() Config { return c.cfg }

// TagSeed exposes the master seed's derivation, for the same reason.
func (c *Chain) TagSeed() (tagkey.Seed, error) { return c.Identity.TagSeed() }

// SecretFor regenerates a tag's bearer secret from its ordinal.
func (c *Chain) SecretFor(ordinal uint64) (tagkey.Secret, error) {
	return c.Identity.SecretFor(ordinal)
}

// WalletKey is the program's own signing key. The command line uses it to
// self-attest a tagging record when no BRC-100 wallet is present -- honestly
// labelled, because the identity key written on chain is then the operator's
// rather than a named biologist's.
func (c *Chain) WalletKey() (*ec.PrivateKey, error) { return c.Identity.WalletKey() }
