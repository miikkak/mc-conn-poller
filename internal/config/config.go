// Package config defines mc-conn-poller's runtime configuration.
package config

import (
	"fmt"
	"net/url"
	"time"
)

// Config is the daemon's full runtime configuration, populated from a YAML
// file, environment variables (MCCP_ prefix), and CLI flags, in that order
// of increasing precedence (viper's usual resolution order).
//
// Targets is deliberately YAML-only, with no flag/env override: a list of
// structs has no sane single-flag representation, the same reasoning
// minecraft-network-watchd applies to its ripe_atlas.asn_labels map.
type Config struct {
	Targets []Target `mapstructure:"targets"`

	// DefaultInterval and DefaultTimeout apply to any target that doesn't
	// set its own Interval/Timeout.
	DefaultInterval time.Duration `mapstructure:"default_interval"`
	DefaultTimeout  time.Duration `mapstructure:"default_timeout"`

	LogLevel string `mapstructure:"log_level"`

	// LogSyslog sends log output to syslog (facility daemon) instead of
	// stderr. Syslog owns the log file's rotation and permissions, avoiding
	// the fd-rotation pitfalls of supervise-daemon's output_log/error_log.
	LogSyslog bool `mapstructure:"log_syslog"`
}

// Target is one protocol-level connectivity check: a host:port to probe on
// its own timer, and the Healthchecks.io check to ping on a successful
// handshake.
type Target struct {
	// Name identifies the target in logs. Must be unique across Targets.
	Name string `mapstructure:"name"`

	// Protocol is "java" (Server List Ping) or "bedrock" (RakNet
	// unconnected ping).
	Protocol string `mapstructure:"protocol"`

	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`

	// IPFamily selects which IP address family probes use: "ipv4" or "ipv6"
	// pins every round to that family, "alternate" (the default when empty)
	// alternates IPv4/IPv6 across rounds. Pinning suits single-stack targets
	// and pollers, and lets IPv4 and IPv6 be monitored by separate targets
	// with separate Healthchecks.io checks.
	IPFamily string `mapstructure:"ip_family"`

	// Interval and Timeout override Config.DefaultInterval/DefaultTimeout
	// when set.
	Interval time.Duration `mapstructure:"interval"`
	Timeout  time.Duration `mapstructure:"timeout"`

	// PingURL is the full Healthchecks.io ping URL for this target's check
	// (e.g. https://hc-ping.com/<uuid>, or a self-hosted instance's
	// ping-key+slug URL — Healthchecks.io always hands you the complete
	// URL to copy, so there's no need to reconstruct one from parts here).
	PingURL string `mapstructure:"ping_url"`
}

const (
	ProtocolJava    = "java"
	ProtocolBedrock = "bedrock"
)

// Valid Target.IPFamily values. An empty IPFamily means IPFamilyAlternate.
const (
	IPFamilyIPv4      = "ipv4"
	IPFamilyIPv6      = "ipv6"
	IPFamilyAlternate = "alternate"
)

// Defaults returns a Config populated with the daemon's default values,
// before any file/env/flag overrides are applied.
func Defaults() Config {
	return Config{
		DefaultInterval: 60 * time.Second,
		DefaultTimeout:  5 * time.Second,
		LogLevel:        "info",
	}
}

// ApplyDefaults resolves each target's effective Interval/Timeout from the
// configured defaults, in place. Call after unmarshalling and before
// Validate.
func (c *Config) ApplyDefaults() {
	for i := range c.Targets {
		if c.Targets[i].Interval <= 0 {
			c.Targets[i].Interval = c.DefaultInterval
		}
		if c.Targets[i].Timeout <= 0 {
			c.Targets[i].Timeout = c.DefaultTimeout
		}
	}
}

// Validate checks that the configuration is complete enough to run. Call
// after ApplyDefaults.
func (c Config) Validate() error {
	if len(c.Targets) == 0 {
		return fmt.Errorf("at least one target must be configured")
	}

	seen := make(map[string]bool, len(c.Targets))
	for _, t := range c.Targets {
		if err := t.validate(); err != nil {
			return fmt.Errorf("target %q: %w", t.Name, err)
		}
		if seen[t.Name] {
			return fmt.Errorf("target name %q is configured more than once", t.Name)
		}
		seen[t.Name] = true
	}
	return nil
}

func (t Target) validate() error {
	if t.Name == "" {
		return fmt.Errorf("name must not be empty")
	}
	if t.Protocol != ProtocolJava && t.Protocol != ProtocolBedrock {
		return fmt.Errorf("protocol must be %q or %q, got %q", ProtocolJava, ProtocolBedrock, t.Protocol)
	}
	if t.Host == "" {
		return fmt.Errorf("host must not be empty")
	}
	switch t.IPFamily {
	case "", IPFamilyIPv4, IPFamilyIPv6, IPFamilyAlternate:
	default:
		return fmt.Errorf("ip_family must be %q, %q or %q, got %q", IPFamilyIPv4, IPFamilyIPv6, IPFamilyAlternate, t.IPFamily)
	}
	if t.Port <= 0 || t.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", t.Port)
	}
	if t.Interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	if t.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if t.Timeout >= t.Interval {
		return fmt.Errorf("timeout (%s) must be less than interval (%s)", t.Timeout, t.Interval)
	}
	if t.PingURL == "" {
		return fmt.Errorf("ping_url must not be empty")
	}
	if u, err := url.ParseRequestURI(t.PingURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		// The value is deliberately not echoed: a ping URL is a bearer secret.
		return fmt.Errorf("ping_url must be an absolute http(s) URL")
	}
	return nil
}
