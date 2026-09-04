package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/export"
)

func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	cfg := commonFlags(fs)
	format := fs.String("format", "csv", "csv | json")
	out := fs.String("o", "", "write to this file instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}

	ctx, cancel := signalContext()
	defer cancel()
	svc, _, closeFn, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()

	w := os.Stdout
	if strings.TrimSpace(*out) != "" {
		f, ferr := os.Create(*out)
		if ferr != nil {
			return fmt.Errorf("open %s: %w", *out, ferr)
		}
		defer f.Close()
		w = f
	}

	switch strings.ToLower(*format) {
	case "csv":
		return export.CSV(ctx, svc.Store(), w)
	case "json":
		return export.JSON(ctx, svc.Store(), w)
	default:
		return fmt.Errorf("unknown format %q; use csv or json", *format)
	}
}
