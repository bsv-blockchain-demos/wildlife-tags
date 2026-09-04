package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/auth"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/web"
)

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfg := commonFlags(fs)
	addr := fs.String("addr", envOr("WILDTAG_ADDR", ":8120"), "listen address")
	adminKeys := fs.String("admin-keys", os.Getenv("WILDTAG_ADMIN_IDENTITY_KEYS"),
		"comma-separated identity keys allowed to administer the program")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}

	ctx, cancel := signalContext()
	defer cancel()

	svc, ch, closeFn, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()

	log := logger()

	// Seed the allowlist from configuration on every start, so a key added to
	// the environment takes effect at a restart rather than requiring a
	// database edit. Keys are only ever added here; removing one from the
	// environment does not revoke it, because a config change that silently
	// locked every biologist out of a live program would be worse than a
	// stale entry.
	for _, key := range strings.Split(*adminKeys, ",") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if err := svc.Store().AddAdmin(ctx, key, "configured"); err != nil {
			return fmt.Errorf("seed admin %s: %w", key, err)
		}
	}

	count, err := svc.Store().CountAdmins(ctx)
	if err != nil {
		return err
	}
	password := os.Getenv("WILDTAG_ADMIN_PASSWORD")
	if count == 0 && password == "" {
		return errors.New(
			"no administrators are configured: set WILDTAG_ADMIN_IDENTITY_KEYS to one or more " +
				"BRC-100 identity keys, or WILDTAG_ADMIN_PASSWORD for the fallback")
	}

	// Cookies are marked Secure unless we are on plain-http localhost, which is
	// the only origin a browser treats as secure without TLS. Config validation
	// has already refused plain http anywhere else.
	secureCookies := strings.HasPrefix(cfg.PublicURL, "https://")

	authn := auth.New(svc.Store(), ch.Wallet, cfg.Originator, password, secureCookies)
	srv, err := web.New(svc, authn, log)
	if err != nil {
		return err
	}

	// A draft lives only in memory, so any tag still marked as redeeming was
	// held by a redemption the previous process took to its grave. Release
	// those before serving, or their rewards are unclaimable forever.
	if n, rerr := svc.ReleaseOrphanedRedemptions(ctx); rerr != nil {
		log.Warn("could not release tags orphaned by the last shutdown", "error", rerr)
	} else if n > 0 {
		log.Info("released tags orphaned by the last shutdown", "count", n)
	}

	// Housekeeping is not optional either: it releases tags whose redemption
	// was abandoned mid-flight by a crabber who lost signal, which otherwise
	// leaves a reward permanently unclaimable.
	go housekeeping(ctx, svc, log)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening",
			"addr", *addr,
			"network", string(cfg.Network),
			"public_url", cfg.PublicURL,
			"administrators", count,
			"password_fallback", password != "")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// housekeeping runs the two loops the program needs to stay honest with itself.
func housekeeping(ctx context.Context, svc interface {
	ExpireDrafts(context.Context) int
}, log interface{ Info(string, ...any) }) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n := svc.ExpireDrafts(ctx); n > 0 {
				log.Info("released abandoned redemptions", "count", n)
			}
		}
	}
}
