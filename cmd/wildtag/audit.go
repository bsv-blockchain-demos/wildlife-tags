package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/audit"
)

func cmdAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	cfg := commonFlags(fs)
	asJSON := fs.Bool("json", false, "emit the report as JSON")
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

	coSign, err := ch.Identity.CoSignPubKeyHex()
	if err != nil {
		return err
	}
	rep, err := audit.Run(ctx, svc.Store(), ch.Storage, coSign)
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return fmt.Errorf("encode report: %w", err)
		}
	} else {
		fmt.Printf("checked %d tags and %d transactions\n\n", rep.TagsChecked, rep.TxsChecked)
		if len(rep.Findings) == 0 {
			fmt.Println("no findings: the chain and the database agree")
		}
		for _, f := range rep.Findings {
			where := f.TagID
			if f.TxID != "" {
				where += " " + f.TxID[:min(12, len(f.TxID))]
			}
			fmt.Printf("  %-8s %-22s %s\n           %s\n", f.Severity, f.Check, where, f.Detail)
		}
	}

	// A non-zero exit on criticals makes this usable as a cron check without a
	// wrapper that has to parse the output.
	if n := rep.Criticals(); n > 0 {
		return fmt.Errorf("%d critical findings", n)
	}
	return nil
}
