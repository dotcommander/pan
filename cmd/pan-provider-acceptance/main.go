package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dotcommander/pan/internal/provideracceptance"
)

func main() {
	var o provideracceptance.Options
	var receipt string
	flag.StringVar(&o.Binary, "pan", "", "path to pan binary (required)")
	flag.StringVar(&o.Config, "config", "", "config path (required)")
	flag.StringVar(&o.Fixtures, "fixtures", "testdata/provider_acceptance", "fixture root")
	flag.StringVar(&o.ReceiptDir, "receipts", ".work/provider-acceptance/runs", "receipt directory")
	flag.StringVar(&o.Repo, "repo", ".", "git repository")
	flag.StringVar(&o.Provider, "provider", "", "provider label")
	flag.StringVar(&o.Model, "model", "", "requested model")
	flag.StringVar(&receipt, "verify-receipt", "", "verify an existing receipt")
	flag.BoolVar(&o.Live, "live-provider", false, "run provider-backed fixture probes (network and provider credentials required)")
	flag.Parse()
	ctx := context.Background()
	if receipt != "" {
		if err := provideracceptance.VerifyReceipt(ctx, receipt, o); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("receipt verified")
		return
	}
	r, err := provideracceptance.Run(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
}
