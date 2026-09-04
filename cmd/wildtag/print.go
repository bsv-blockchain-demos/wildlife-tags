package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/qr"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

func cmdPrint(args []string) error {
	fs := flag.NewFlagSet("print", flag.ExitOnError)
	cfg := commonFlags(fs)
	batchID := fs.String("batch", "", "batch id")
	out := fs.String("o", "", "write to this file instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}
	if strings.TrimSpace(*batchID) == "" {
		return fmt.Errorf("-batch is required")
	}

	ctx, cancel := signalContext()
	defer cancel()
	svc, _, closeFn, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()

	batch, err := svc.Store().GetBatch(ctx, *batchID)
	if err != nil {
		return err
	}
	tags, err := svc.Store().TagsByBatch(ctx, *batchID)
	if err != nil {
		return err
	}

	sheet := qr.Sheet{
		BatchID:   batch.ID,
		CreatedAt: batch.CreatedAt.Format(time.RFC1123),
		PublicURL: cfg.PublicURL,
	}
	for i, t := range tags {
		secret, err := svc.SecretFor(t.Ordinal)
		if err != nil {
			return err
		}
		id := tagkey.ID(t.TagID)
		payload := svc.QRPayload(id, secret)
		code, err := qr.Encode(payload)
		if err != nil {
			return err
		}
		sheet.Tags = append(sheet.Tags, qr.SheetTag{
			TagID:    t.TagID,
			Display:  id.Display(),
			Ordinal:  t.Ordinal,
			Payload:  payload,
			Code:     code,
			Position: i + 1,
		})
	}

	w := os.Stdout
	if strings.TrimSpace(*out) != "" {
		// 0600: every QR code on this sheet is a bearer instrument.
		f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("open %s: %w", *out, err)
		}
		defer f.Close()
		w = f
	}
	if err := qr.Render(w, sheet); err != nil {
		return err
	}
	if w != os.Stdout {
		fmt.Fprintf(os.Stderr, "wrote %d tags to %s\n", len(sheet.Tags), *out)
		fmt.Fprintf(os.Stderr, "Every QR code in that file is a bearer instrument. Delete it after printing.\n")
	}
	return nil
}

func cmdSecrets(args []string) error {
	fs := flag.NewFlagSet("secrets", flag.ExitOnError)
	cfg := commonFlags(fs)
	batchID := fs.String("batch", "", "batch id")
	out := fs.String("o", "", "write to this file (required; this is not printed to a terminal)")
	actor := fs.String("by", "operator", "who is exporting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}
	if strings.TrimSpace(*batchID) == "" || strings.TrimSpace(*out) == "" {
		return fmt.Errorf("-batch and -o are both required")
	}

	ctx, cancel := signalContext()
	defer cancel()
	svc, _, closeFn, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()

	batch, err := svc.Store().GetBatch(ctx, *batchID)
	if err != nil {
		return err
	}
	if batch.SecretsAt != nil {
		return fmt.Errorf("batch %s already had its secrets exported on %s; a second export is refused",
			batch.ID, batch.SecretsAt.Format(time.RFC1123))
	}
	tags, err := svc.Store().TagsByBatch(ctx, *batchID)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", *out, err)
	}
	defer f.Close()

	fmt.Fprintln(f, "tag_id,ordinal,qr_payload")
	for _, t := range tags {
		secret, serr := svc.SecretFor(t.Ordinal)
		if serr != nil {
			return serr
		}
		fmt.Fprintf(f, "%s,%d,%s\n", t.TagID, t.Ordinal, svc.QRPayload(tagkey.ID(t.TagID), secret))
	}

	if err := svc.Store().MarkSecretsExported(ctx, batch.ID); err != nil {
		return err
	}
	if err := svc.Store().Audit(ctx, *actor, "batch.secrets.export", fmt.Sprintf("%s to %s", batch.ID, *out)); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "wrote %d tag secrets to %s\n\n", len(tags), *out)
	fmt.Fprint(os.Stderr, `Each line in that file can redeem a tag. Treat it like cash:
move it to whatever prints the tags, print, then delete it.

This export is recorded in the audit log and will not run again for this batch.
The secrets remain derivable from the master seed, so nothing is lost by
deleting the file -- that is the whole reason tag keys are derived rather than
random.
`)
	return nil
}
