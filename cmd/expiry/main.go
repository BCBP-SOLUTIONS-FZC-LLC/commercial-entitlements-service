// Package main triggers the ExpiryFanout Temporal workflow which expires
// overrides and addon packs, then recomputes affected tenants.
package main

import (
	"context"
	"fmt"
	"os"

	temporalclient "go.temporal.io/sdk/client"

	temporaladapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/temporal"
)

func main() {
	tempAddr := os.Getenv("TEMPORAL_HOST_PORT")
	if tempAddr == "" {
		tempAddr = "localhost:7233"
	}

	tc, err := temporalclient.Dial(temporalclient.Options{HostPort: tempAddr})
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect temporal: %v\n", err)
		os.Exit(1)
	}
	defer tc.Close()

	starter := temporaladapter.NewStarter(tc)
	if err := starter.StartExpiryFanout(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "expiry fanout failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("expiry fanout workflow started")
}
