// Command mc-conn-poller probes the Minecraft proxy at the protocol level
// from an external vantage point and reports success to Healthchecks.io.
package main

import (
	"fmt"
	"os"

	"github.com/miikkak/mc-conn-poller/internal/cmd"
)

var version = "dev"

func main() {
	cmd.RootCmd.Version = version
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
