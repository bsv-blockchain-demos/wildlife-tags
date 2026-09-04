package main

import (
	"flag"
	"fmt"
)

// cmdReclaim recovers outputs the wallet recorded but never minted as
// spendable.
//
// A wallet mints only change into its UTXO pool. Any output an application
// names for itself is assumed to be the application's to manage and is recorded
// without becoming spendable — which is exactly what keeps tag outputs out of
// the funding pool. The failure mode is an application that pays itself by
// naming an output: the balance moves and the spendable pool does not.
func cmdReclaim(args []string) error {
	fs := flag.NewFlagSet("reclaim", flag.ExitOnError)
	cfg := commonFlags(fs)
	dryRun := fs.Bool("dry-run", true, "list what is stranded without spending anything")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := finishConfig(fs, cfg); err != nil {
		return err
	}

	ctx, cancel := signalContext()
	defer cancel()
	_, ch, closeFn, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeFn()

	stranded, err := ch.FindStranded(ctx)
	if err != nil {
		return err
	}
	if len(stranded) == 0 {
		fmt.Println("nothing is stranded")
		return nil
	}

	var total uint64
	for _, o := range stranded {
		total += o.Satoshis
		fmt.Printf("  %s:%d  %d satoshis  %s\n", o.TxID[:16], o.Vout, o.Satoshis, o.Description)
	}
	fmt.Printf("\n%d outputs holding %d satoshis the wallet cannot currently spend\n", len(stranded), total)

	if *dryRun {
		fmt.Println("\nRe-run with -dry-run=false to sweep them back into spendable change.")
		return nil
	}

	txid, reclaimed, err := ch.Reclaim(ctx, stranded)
	if err != nil {
		return err
	}
	fmt.Printf("\nreclaimed %d satoshis at %s\n", reclaimed, txid)
	return nil
}
