// Package cmd implements mc-conn-poller's command-line interface.
package cmd

import "github.com/spf13/cobra"

var RootCmd = &cobra.Command{
	Use:   "mc-conn-poller",
	Short: "External protocol-level connectivity poller for the Minecraft proxy",
}

func Execute() error {
	return RootCmd.Execute()
}
