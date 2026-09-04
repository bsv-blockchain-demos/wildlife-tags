package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bsv-blockchain/go-arcade-toolbox/pkg/arcade"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/chain"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/service"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	cfg := commonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}

	id, err := chain.CreateIdentity(cfg.DataDir)
	if err != nil {
		return err
	}
	coSign, err := id.CoSignPubKeyHex()
	if err != nil {
		return err
	}
	identity, err := id.WalletIdentityKeyHex()
	if err != nil {
		return err
	}

	fmt.Printf("Wrote %s\n\n", cfg.DataDir+"/keys.json")
	fmt.Printf("  wallet identity key   %s\n", identity)
	fmt.Printf("  DNR co-signing key    %s\n\n", coSign)
	fmt.Print(`This file holds the master seed every tag key in the program derives from.
Whoever has it can spend any tag that has ever been printed, and losing it
loses the ability to reclaim rewards from tags that are never reported.

Back it up somewhere that is not this machine, and keep it out of git.
`)
	return nil
}

func cmdAddress(args []string) error {
	fs := flag.NewFlagSet("address", flag.ExitOnError)
	cfg := commonFlags(fs)
	// A BRC-100 wallet pays a locking script, not an address. Printing the
	// script is what lets a desktop wallet fund the program without anyone
	// hand-assembling opcodes.
	showScript := fs.Bool("script", false, "also print the deposit locking script, for paying from a BRC-100 wallet")
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

	addr, err := svc.Chain().DepositAddress()
	if err != nil {
		return err
	}
	balance, err := svc.Chain().Balance(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("deposit address  %s\n", addr)
	if *showScript {
		script, serr := chain.DepositLockingScriptHex(addr, cfg.Mainnet())
		if serr != nil {
			return serr
		}
		fmt.Printf("locking script   %s\n", script)
	}
	fmt.Printf("balance          %d satoshis\n", balance)
	fmt.Printf("reward per tag   %d base + %d bonus = %d satoshis\n",
		cfg.BaseSatoshis, cfg.BonusSatoshis, cfg.TotalReward())
	if cfg.TotalReward() > 0 {
		fmt.Printf("funds about      %d activations\n", balance/cfg.TotalReward())
	}
	return nil
}

func cmdFund(args []string) error {
	fs := flag.NewFlagSet("fund", flag.ExitOnError)
	cfg := commonFlags(fs)
	rawHex := fs.String("tx", "", "an already-mined funding transaction, hex")
	beefHex := fs.String("beef", "", "BEEF from a wallet's noSend createAction; broadcast through our own arcade, then credited once mined")
	beefFile := fs.String("beef-file", "", "read the BEEF hex from a file rather than the command line")
	txidOnly := fs.String("txid", "", "resume: a transaction already broadcast through this arcade; wait for its proof and credit it")
	proofHex := fs.String("proof", "", "merkle proof hex; fetched from arcade when omitted")
	wait := fs.Duration("wait", 30*time.Minute, "how long to wait for the funding transaction to be mined")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}

	if *beefFile != "" {
		body, rerr := os.ReadFile(*beefFile)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", *beefFile, rerr)
		}
		*beefHex = strings.TrimSpace(string(body))
	}
	if strings.TrimSpace(*rawHex) == "" && strings.TrimSpace(*beefHex) == "" && strings.TrimSpace(*txidOnly) == "" {
		return fmt.Errorf("one of -tx, -beef, -beef-file or -txid is required")
	}

	ctx, cancel := signalContext()
	defer cancel()
	_, ch, closeFn, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()

	// Resuming a broadcast that is still waiting for a block. Safe to re-run:
	// it submits nothing.
	if id := strings.TrimSpace(*txidOnly); id != "" {
		waitCtx, stop := context.WithTimeout(ctx, *wait)
		defer stop()

		last := ""
		total, rerr := ch.ResumeFunding(waitCtx, id, 15*time.Second, func(st arcade.Status) {
			if string(st) != last {
				last = string(st)
				fmt.Printf("  %s  %s\n", time.Now().Format("15:04:05"), last)
			}
		})
		if rerr != nil {
			return rerr
		}
		fmt.Printf("imported %d satoshis\n", total)
		return nil
	}

	// The -beef path is what a BRC-100 wallet feeds. It broadcasts through our
	// own arcade rather than trusting the wallet to have sent it to the network
	// we are watching: BRC-100's getNetwork answers only "testnet", which does
	// not distinguish one teranode test network from another, and a funding
	// transaction sent to the wrong one is invisible here forever.
	if b := strings.TrimSpace(*beefHex); b != "" {
		blob, derr := hex.DecodeString(b)
		if derr != nil {
			return fmt.Errorf("-beef is not hex: %w", derr)
		}

		waitCtx, stop := context.WithTimeout(ctx, *wait)
		defer stop()

		last := ""
		txid, total, ferr := ch.FundFromForeign(waitCtx, blob, 15*time.Second, func(st arcade.Status) {
			if string(st) != last {
				last = string(st)
				fmt.Printf("  %s  %s\n", time.Now().Format("15:04:05"), last)
			}
		})
		if txid != "" {
			fmt.Printf("txid %s\n", txid)
		}
		if ferr != nil {
			return ferr
		}
		fmt.Printf("imported %d satoshis\n", total)
		return nil
	}

	raw, err := hex.DecodeString(strings.TrimSpace(*rawHex))
	if err != nil {
		return fmt.Errorf("-tx is not hex: %w", err)
	}
	total, err := ch.ImportFunding(ctx, raw, strings.TrimSpace(*proofHex))
	if err != nil {
		return err
	}
	fmt.Printf("imported %d satoshis\n", total)
	return nil
}

func cmdMkBatch(args []string) error {
	fs := flag.NewFlagSet("mkbatch", flag.ExitOnError)
	cfg := commonFlags(fs)
	count := fs.Int("n", 0, "how many tags to create")
	sp := fs.String("species", "", "species code this run is printed for (default "+species.Default+")")
	actor := fs.String("by", "operator", "who is creating this batch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}
	if *count <= 0 {
		return fmt.Errorf("-n is required")
	}

	ctx, cancel := signalContext()
	defer cancel()
	svc, _, closeFn, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()

	batch, tags, err := svc.MintBatch(ctx, *count, *sp, *actor)
	if err != nil {
		return err
	}
	fmt.Printf("batch %s: %d %s tags from ordinal %d\n", batch.ID, batch.TagCount, batch.Species, batch.FirstOrdinal)
	fmt.Printf("manifest %s\n\n", batch.ManifestHash)
	for _, t := range tags {
		fmt.Printf("  %s\n", tagkey.ID(t.TagID).Display())
	}
	fmt.Printf("\nRender the printable sheet with:\n  wildtag print -batch %s > %s.html\n", batch.ID, batch.ID)
	return nil
}

func cmdActivate(args []string) error {
	fs := flag.NewFlagSet("activate", flag.ExitOnError)
	cfg := commonFlags(fs)
	tag := fs.String("tag", "", "tag id")
	sp := fs.String("species", "", "species code (default: the batch's, then "+species.Default+")")
	lat := fs.Float64("lat", 0, "latitude, decimal degrees")
	lon := fs.Float64("lon", 0, "longitude, decimal degrees")
	acc := fs.Float64("acc", 5, "position accuracy, metres")
	meas := fs.String("meas", "", "measurements, as key=value pairs: cw=150,wt=2210")
	attr := fs.String("attr", "", "attributes, as key=value pairs: sex=M,stage=HARD,gear=TRAP")
	describe := fs.Bool("describe", false, "print what this species records, and stop")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *describe {
		return describeSpecies(*sp)
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}

	id, err := tagkey.ParseID(*tag)
	if err != nil {
		return err
	}
	measures, err := parseIntPairs(*meas)
	if err != nil {
		return err
	}
	attributes, err := parsePairs(*attr)
	if err != nil {
		return err
	}
	if len(measures) == 0 {
		return fmt.Errorf("-meas is required: a tagging record without a measurement is not worth writing.\n" +
			"Run with -describe to see what this species records")
	}

	ctx, cancel := signalContext()
	defer cancel()
	svc, ch, closeFn, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()

	// Default to what the tag's own print run was made for, so a single-species
	// deployment never has to name it and a mixed one gets the right answer
	// without the operator remembering which sheet a tag came off.
	code := *sp
	if code == "" {
		if t, terr := svc.Store().GetTag(ctx, string(id)); terr == nil {
			if b, berr := svc.Store().GetBatch(ctx, t.BatchID); berr == nil {
				code = b.Species
			}
		}
	}

	// The command line has no BRC-100 wallet, so the record is attested with
	// the program's own wallet key. That is honestly recorded: the identity key
	// written on chain is the operator's, so anyone reading the dataset can
	// tell which activations came from a person and which from a console.
	//
	// The key is needed before the record is built, not after. It is written
	// *inside* the bytes being signed, so requesting the record without it
	// yields one record to sign and a different one to submit -- which the
	// server correctly refuses as a mismatch.
	walletKey, err := ch.Identity.WalletKey()
	if err != nil {
		return err
	}
	pubHex := hex.EncodeToString(walletKey.PubKey().Compressed())

	in := service.ActivationInput{
		TagID:        id,
		Species:      code,
		Lat:          *lat,
		Lon:          *lon,
		AccuracyM:    *acc,
		Meas:         measures,
		Attr:         attributes,
		AttestPubHex: pubHex,
	}

	preview, err := svc.PrepareActivation(ctx, in)
	if err != nil {
		return err
	}

	sig, _, err := service.SelfAttest(preview.Observation, walletKey, string(id))
	if err != nil {
		return err
	}
	in.Observation, in.AttestSig = preview.Observation, sig

	res, err := svc.Activate(ctx, in)
	if err != nil {
		return err
	}
	fmt.Printf("armed %s as %s with %d satoshis\n", id.Display(), preview.Species, res.Satoshis)
	fmt.Printf("  txid        %s\n", res.TxID)
	fmt.Printf("  sweepable   %s\n", res.SweepAfter.Format(time.RFC1123))
	return nil
}

// describeSpecies prints what a profile records, so an operator can fill in
// -meas and -attr without reading the JSON.
func describeSpecies(code string) error {
	if code == "" {
		profiles, err := species.All()
		if err != nil {
			return err
		}
		fmt.Println("species this deployment knows about:")
		for _, p := range profiles {
			fmt.Printf("  %-8s %s (%s), %s\n", p.Code, p.Common, p.Scientific, p.Workflow)
		}
		fmt.Println("\nRun with -species <code> -describe for one species' fields.")
		return nil
	}

	p, err := species.Get(code)
	if err != nil {
		return err
	}
	fmt.Printf("%s -- %s (%s)\n", p.Code, p.Common, p.Scientific)
	fmt.Printf("%s, %s\n\n", p.Programme, p.Workflow)

	fmt.Println("-meas")
	for _, m := range p.Measures {
		req := ""
		if m.Required {
			req = "  (required)"
		}
		fmt.Printf("  %-6s %s, %s, %d-%d%s\n", m.Key, m.Label, m.Unit, m.Min, m.Max, req)
		if m.Scale > 1 {
			fmt.Printf("         recorded as whole %s of a %s\n", scaleWord(m.Scale), m.Unit)
		}
	}
	fmt.Println("\n-attr")
	for _, v := range p.Vocabs {
		req := ""
		if v.Required {
			req = "  (required)"
		}
		fmt.Printf("  %-6s %s%s\n", v.Key, v.Label, req)
		for _, val := range v.Values {
			fmt.Printf("           %-14s %s\n", val.Code, val.Label)
		}
	}
	return nil
}

func scaleWord(scale int) string {
	switch scale {
	case 100:
		return "hundredths"
	case 1000:
		return "thousandths"
	}
	return fmt.Sprintf("1/%d", scale)
}

// parsePairs reads "a=1,b=2" into a map.
//
// A malformed pair is an error rather than a skipped field: silently dropping
// half a measurement would put a record on chain that says less than the
// operator thought it did, permanently.
func parsePairs(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("%q is not a key=value pair", pair)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

// parseIntPairs is parsePairs for scaled integers.
func parseIntPairs(s string) (map[string]int, error) {
	pairs, err := parsePairs(s)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(pairs))
	for k, v := range pairs {
		n, cerr := strconv.Atoi(v)
		if cerr != nil {
			return nil, fmt.Errorf("%s=%s is not a whole number; measurements are scaled integers, never decimals", k, v)
		}
		out[k] = n
	}
	return out, nil
}

// cmdRelease frees wallet inputs left reserved by a redemption that was never
// completed.
//
// CreateAction reserves coins as soon as it hands back a signable transaction.
// The running server releases them on every abandonment path it knows about,
// but a process killed between those points leaves the reservation behind, and
// the wallet then reports a balance it cannot actually spend. The reference is
// the reserved_by value on the stuck row.
func cmdRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	cfg := commonFlags(fs)
	ref := fs.String("ref", "", "the reservation reference holding the inputs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}
	if strings.TrimSpace(*ref) == "" {
		return fmt.Errorf("-ref is required")
	}

	ctx, cancel := signalContext()
	defer cancel()
	_, ch, closeFn, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()

	if err := ch.AbortDraft(ctx, []byte(*ref)); err != nil {
		return err
	}
	balance, err := ch.Balance(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("released %s\nspendable balance now %d satoshis\n", *ref, balance)
	return nil
}

func cmdRearm(args []string) error {
	fs := flag.NewFlagSet("rearm", flag.ExitOnError)
	cfg := commonFlags(fs)
	tag := fs.String("tag", "", "tag id; omit to list what is waiting")
	actor := fs.String("by", "operator", "who is re-arming")
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

	if strings.TrimSpace(*tag) == "" {
		pending, err := svc.PendingRearms(ctx, 200)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			fmt.Println("no tags are waiting to be re-armed")
			return nil
		}
		fmt.Printf("%d tags are waiting to be re-armed:\n", len(pending))
		for _, t := range pending {
			fmt.Printf("  %s  generation %d  %d satoshis\n", tagkey.ID(t.TagID).Display(), t.Generation, t.LiveSatoshis)
		}
		return nil
	}

	id, err := tagkey.ParseID(*tag)
	if err != nil {
		return err
	}
	if err := svc.Rearm(ctx, string(id), *actor); err != nil {
		return err
	}
	fmt.Printf("%s is back in service\n", id.Display())
	return nil
}

func cmdSweep(args []string) error {
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	cfg := commonFlags(fs)
	limit := fs.Int("limit", 50, "how many tags to sweep in one run")
	tag := fs.String("tag", "", "reclaim this one tag now, regardless of its sweep date")
	dryRun := fs.Bool("dry-run", true, "list what would be swept without spending anything")
	actor := fs.String("by", "operator", "who is sweeping")
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

	// Reclaiming one named tag ignores the date, so it also ignores -dry-run:
	// naming a specific tag is already the deliberate act the flag protects
	// against.
	if id := strings.TrimSpace(*tag); id != "" {
		parsed, perr := tagkey.ParseID(id)
		if perr != nil {
			return perr
		}
		res, serr := svc.SweepTag(ctx, string(parsed), *actor)
		if serr != nil {
			return serr
		}
		if res.Err != "" {
			return fmt.Errorf("%s: %s", parsed.Display(), res.Err)
		}
		fmt.Printf("reclaimed %d satoshis from %s at %s\n", res.Satoshis, parsed.Display(), res.TxID)
		return nil
	}

	if *dryRun {
		tags, err := svc.Store().SweepableTags(ctx, time.Now().UTC(), *limit)
		if err != nil {
			return err
		}
		if len(tags) == 0 {
			fmt.Println("nothing is sweepable")
			return nil
		}
		var total uint64
		fmt.Printf("%d tags are past their sweep date:\n", len(tags))
		for _, t := range tags {
			total += t.LiveSatoshis
			fmt.Printf("  %s  %d satoshis  armed %s\n", tagkey.ID(t.TagID).Display(), t.LiveSatoshis, whenOr(t.ActivatedAt))
		}
		fmt.Printf("\n%d satoshis would be reclaimed. Re-run with -dry-run=false to do it.\n", total)
		return nil
	}

	results, err := svc.SweepExpired(ctx, *limit, *actor)
	if err != nil {
		return err
	}
	var reclaimed uint64
	for _, r := range results {
		if r.Err != "" {
			fmt.Fprintf(os.Stderr, "  %s failed: %s\n", r.TagID, r.Err)
			continue
		}
		reclaimed += r.Satoshis
		fmt.Printf("  %s reclaimed %d satoshis at %s\n", tagkey.ID(r.TagID).Display(), r.Satoshis, r.TxID)
	}
	fmt.Printf("\nreclaimed %d satoshis from %d tags\n", reclaimed, len(results))
	return nil
}

func whenOr(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.Format("2006-01-02")
}
