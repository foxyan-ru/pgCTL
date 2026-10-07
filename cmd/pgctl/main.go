// Command pgctl is a Postgres lifecycle control plane for local Docker
// clusters, with safe major-version upgrade validation as its flagship
// feature. See the repo README for the full walkthrough.
package main

import (
	"fmt"
	"os"

	"pgctl/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
