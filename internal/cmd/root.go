// Package cmd wires the daemon's CLI flags/config file into a run loop.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/miikkak/mc-conn-poller/internal/config"
	"github.com/miikkak/mc-conn-poller/internal/poller"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// RootCmd is the base command: a long-running daemon with no subcommands.
var RootCmd = &cobra.Command{
	Use:   "mc-conn-poller",
	Short: "External protocol-level connectivity poller for the Minecraft proxy",
	Long: `mc-conn-poller probes the Minecraft proxy at the protocol level (Java
Server List Ping, Bedrock RakNet unconnected ping) from this host and
reports each successful handshake to the target's Healthchecks.io check.

Targets are configured only via the YAML config file (see --config); there
is deliberately no per-target flag or MCCP_ env var, the same way
minecraft-network-watchd keeps its ripe_atlas.asn_labels map YAML-only —
a list of structs has no sane single-flag representation.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if configReadErr != nil {
			return fmt.Errorf("reading configuration: %w", configReadErr)
		}

		var cfg config.Config
		if err := viper.Unmarshal(&cfg); err != nil {
			return fmt.Errorf("parsing configuration: %w", err)
		}
		cfg.ApplyDefaults()
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		}

		logger, err := newLogger(cfg.LogLevel, cfg.LogSyslog)
		if err != nil {
			return err
		}
		slog.SetDefault(logger)

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		info := poller.Info{Version: cmd.Version, Host: hostname}

		return poller.Run(ctx, cfg, logger, info)
	},
}

// Execute runs the root command. Called once from main.main.
func Execute() error {
	return RootCmd.ExecuteContext(context.Background())
}

func init() {
	cobra.OnInitialize(initConfig)

	def := config.Defaults()

	RootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: /usr/local/etc/mc-conn-poller.yaml)")
	RootCmd.PersistentFlags().Duration("default-interval", def.DefaultInterval, "poll interval used for any target that doesn't set its own")
	RootCmd.PersistentFlags().Duration("default-timeout", def.DefaultTimeout, "probe timeout used for any target that doesn't set its own")
	RootCmd.PersistentFlags().String("log-level", def.LogLevel, "log level: debug, info, warn, error")
	RootCmd.PersistentFlags().Bool("log-syslog", def.LogSyslog, "send logs to syslog (facility daemon) instead of stderr")

	bind := map[string]string{
		"default_interval": "default-interval",
		"default_timeout":  "default-timeout",
		"log_level":        "log-level",
		"log_syslog":       "log-syslog",
	}
	for key, flag := range bind {
		if err := viper.BindPFlag(key, RootCmd.PersistentFlags().Lookup(flag)); err != nil {
			panic(err)
		}
	}
}

// configReadErr holds any error from initConfig's viper.ReadInConfig other
// than "file not found" — RunE reports it before Validate gets a chance to
// produce a misleading "at least one target must be configured" for what's
// actually a YAML syntax error. cobra.OnInitialize hooks can't return an
// error directly, hence the package var.
var configReadErr error

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("mc-conn-poller")
		viper.SetConfigType("yaml")
		viper.AddConfigPath("/usr/local/etc")
		viper.AddConfigPath("/etc")
	}

	viper.SetEnvPrefix("mccp")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			configReadErr = err
		}
	}
}

func newLogger(level string, useSyslog bool) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}

	if useSyslog {
		h, err := newSyslogHandler("mc-conn-poller", lvl)
		if err != nil {
			return nil, fmt.Errorf("initializing syslog logger: %w", err)
		}
		return slog.New(h), nil
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})), nil
}
