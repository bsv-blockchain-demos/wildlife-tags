// Command wildtag runs the SCDNR blue crab tag reward program.
//
// Subcommands, roughly in the order a deployment uses them:
//
//	init      mint keys.json for a fresh deployment
//	address   print the deposit address to fund the program
//	fund      import a mined funding transaction
//	mkbatch   create a run of tags
//	print     render a printable QR sheet for a batch
//	secrets   export a batch's tag secrets, once
//	activate  arm a tag from the command line
//	rearm     bring a cooled-down tag back into service
//	sweep     reclaim rewards from tags that were never reported
//	export    write the public dataset
//	audit     reconcile the chain against the database
//	serve     run the web application
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/defs"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/chain"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/service"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "wildtag: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	mode := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}

	switch mode {
	case "help", "-h", "--help":
		usage()
		return nil
	case "init":
		return cmdInit(args)
	case "address":
		return cmdAddress(args)
	case "fund":
		return cmdFund(args)
	case "mkbatch":
		return cmdMkBatch(args)
	case "print":
		return cmdPrint(args)
	case "secrets":
		return cmdSecrets(args)
	case "activate":
		return cmdActivate(args)
	case "rearm":
		return cmdRearm(args)
	case "release":
		return cmdRelease(args)
	case "sweep":
		return cmdSweep(args)
	case "reclaim":
		return cmdReclaim(args)
	case "export":
		return cmdExport(args)
	case "audit":
		return cmdAudit(args)
	case "serve":
		return cmdServe(args)
	default:
		usage()
		return fmt.Errorf("unknown command %q", mode)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `wildtag - QR-unlockable bounties for DNR wildlife tag returns

usage: wildtag <command> [flags]

  init       mint keys.json for a fresh deployment (refuses to overwrite)
  address    print the deposit address that funds the program
  fund       import a mined funding transaction
  mkbatch    create a run of tags            -n <count> [-species <code>]
  print      render a printable QR sheet     -batch <id>
  secrets    export a batch's tag secrets, once, to a file
  activate   arm a tag from the command line -tag <id> -lat -lon -meas -attr
             (run "wildtag activate -describe" to list the species it knows)
  rearm      return a cooled-down tag to service  -tag <id>
  release    free wallet inputs stuck on an abandoned redemption  -ref <ref>
  sweep      reclaim rewards from tags never reported
  reclaim    recover outputs the wallet recorded but cannot spend
  export     write the public dataset        -format csv|json
  audit      reconcile the chain against the database
  serve      run the web application

Common flags (environment variable in brackets):
  -data-dir      [WILDTAG_DATA_DIR]      where keys.json and the databases live
  -network       [WILDTAG_NETWORK]       main | test | ttn | tstn
  -arcade-url    [WILDTAG_ARCADE_URL]    the transaction oracle
  -public-url    [WILDTAG_PUBLIC_URL]    the origin printed into every QR code
  -postgres-dsn  [WILDTAG_POSTGRES_DSN]  empty means SQLite
  -addr          [WILDTAG_ADDR]          listen address for serve

The public URL is printed into every QR code. Changing it after a batch has
been printed makes those tags point at a host that no longer serves them, so it
is validated at startup and recorded with each batch.
`)
}

// commonFlags binds the configuration every subcommand shares. Environment
// variables supply the defaults so a deployment can set them once; flags win,
// so an operator can override one without editing the environment.
func commonFlags(fs *flag.FlagSet) *chain.Config {
	cfg := chain.DefaultConfig()
	network := fs.String("network", envOr("WILDTAG_NETWORK", string(defs.NetworkTSTN)), "main | test | ttn | tstn")
	fs.StringVar(&cfg.ArcadeURL, "arcade-url", os.Getenv("WILDTAG_ARCADE_URL"), "arcade base URL")
	fs.StringVar(&cfg.ChainTracksURL, "chaintracks-url", os.Getenv("WILDTAG_CHAINTRACKS_URL"), "chaintracks base URL")
	fs.StringVar(&cfg.DataDir, "data-dir", envOr("WILDTAG_DATA_DIR", "./data"), "data directory")
	fs.StringVar(&cfg.PostgresDSN, "postgres-dsn", os.Getenv("WILDTAG_POSTGRES_DSN"), "postgres DSN; empty means SQLite")
	fs.StringVar(&cfg.PublicURL, "public-url", envOr("WILDTAG_PUBLIC_URL", "http://localhost:8120"), "origin printed into QR codes")
	fs.Uint64Var(&cfg.BaseSatoshis, "base-sats", uint64(envInt("WILDTAG_BASE_SATS", 5000)), "reward for reporting a tag")
	fs.Uint64Var(&cfg.BonusSatoshis, "bonus-sats", uint64(envInt("WILDTAG_BONUS_SATS", 15000)), "re-release bonus, escrowed")

	// Resolved in finishConfig, once the flags have been parsed.
	cfg.Network = defs.BSVNetwork(*network)
	return &cfg
}

func finishConfig(fs *flag.FlagSet, cfg *chain.Config) error {
	if v := fs.Lookup("network"); v != nil {
		n, err := defs.ParseBSVNetworkStr(v.Value.String())
		if err != nil {
			return fmt.Errorf("network: %w", err)
		}
		cfg.Network = n
	}
	return cfg.Validate()
}

func logger() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("WILDTAG_DEBUG") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// open wires everything a subcommand that touches money needs.
//
// It returns the concrete chain alongside the service because a few
// subcommands legitimately need the wallet itself -- importing funding,
// reading raw transactions for the audit, verifying an administrator's
// signature -- and widening the service's Ledger interface to cover them would
// defeat the point of having a narrow one.
func open(ctx context.Context, cfg *chain.Config) (*service.Service, *chain.Chain, func(), error) {
	id, err := chain.LoadIdentity(cfg.DataDir)
	if err != nil {
		if errors.Is(err, chain.ErrNoIdentity) {
			return nil, nil, nil, fmt.Errorf("%w\n\nRun `wildtag init` to mint one", err)
		}
		return nil, nil, nil, err
	}

	log := logger()
	c, err := chain.Open(ctx, log, *cfg, id)
	if err != nil {
		return nil, nil, nil, err
	}

	st, err := store.Open(ctx, filepath.Join(cfg.DataDir, "tags.db"), cfg.PostgresDSN)
	if err != nil {
		_ = c.Close(ctx)
		return nil, nil, nil, err
	}

	svc := service.New(c, st, log)
	svc.FollowChainStatus(ctx)

	return svc, c, func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := st.Close(); err != nil {
			log.Warn("closing the application database", "error", err)
		}
		if err := c.Close(closeCtx); err != nil {
			log.Warn("closing the wallet", "error", err)
		}
	}, nil
}

// signalContext cancels on SIGINT or SIGTERM so a subcommand mid-broadcast gets
// the chance to finish writing what it knows.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		// Configuration comes from ConfigMaps and shell profiles, where a typo
		// should not stop a pod from starting with a sane default.
		fmt.Fprintf(os.Stderr, "wildtag: %s=%q is not a number; using %d\n", key, v, fallback)
		return fallback
	}
	return n
}
