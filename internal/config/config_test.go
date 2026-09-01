package config

import (
	"testing"
	"time"
)

func validTarget() Target {
	return Target{
		Name:     "proxy-java",
		Protocol: ProtocolJava,
		Host:     "mc.example.com",
		Port:     25565,
		Interval: time.Minute,
		Timeout:  5 * time.Second,
		PingURL:  "https://hc-ping.com/00000000-0000-0000-0000-000000000000",
	}
}

func TestApplyDefaultsFillsUnsetFields(t *testing.T) {
	cfg := Defaults()
	target := validTarget()
	target.Interval = 0
	target.Timeout = 0
	cfg.Targets = []Target{target}

	cfg.ApplyDefaults()

	if cfg.Targets[0].Interval != cfg.DefaultInterval {
		t.Errorf("Interval = %s, want default %s", cfg.Targets[0].Interval, cfg.DefaultInterval)
	}
	if cfg.Targets[0].Timeout != cfg.DefaultTimeout {
		t.Errorf("Timeout = %s, want default %s", cfg.Targets[0].Timeout, cfg.DefaultTimeout)
	}
}

func TestApplyDefaultsLeavesExplicitValuesAlone(t *testing.T) {
	cfg := Defaults()
	target := validTarget()
	target.Interval = 30 * time.Second
	target.Timeout = 2 * time.Second
	cfg.Targets = []Target{target}

	cfg.ApplyDefaults()

	if cfg.Targets[0].Interval != 30*time.Second {
		t.Errorf("Interval = %s, want unchanged 30s", cfg.Targets[0].Interval)
	}
	if cfg.Targets[0].Timeout != 2*time.Second {
		t.Errorf("Timeout = %s, want unchanged 2s", cfg.Targets[0].Timeout)
	}
}

func TestValidateAcceptsWellFormedConfig(t *testing.T) {
	cfg := Config{Targets: []Target{validTarget()}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsNoTargets(t *testing.T) {
	cfg := Config{}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() = nil, want error for empty target list")
	}
}

func TestValidateRejectsDuplicateNames(t *testing.T) {
	a, b := validTarget(), validTarget()
	cfg := Config{Targets: []Target{a, b}}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() = nil, want error for duplicate target names")
	}
}

func TestValidateTarget(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Target)
	}{
		{"empty name", func(tt *Target) { tt.Name = "" }},
		{"bad protocol", func(tt *Target) { tt.Protocol = "http" }},
		{"empty host", func(tt *Target) { tt.Host = "" }},
		{"port zero", func(tt *Target) { tt.Port = 0 }},
		{"port too large", func(tt *Target) { tt.Port = 70000 }},
		{"non-positive interval", func(tt *Target) { tt.Interval = 0 }},
		{"non-positive timeout", func(tt *Target) { tt.Timeout = 0 }},
		{"empty ping_url", func(tt *Target) { tt.PingURL = "" }},
		{"relative ping_url", func(tt *Target) { tt.PingURL = "/not-absolute" }},
		{"non-http(s) ping_url", func(tt *Target) { tt.PingURL = "ftp://hc-ping.com/00000000-0000-0000-0000-000000000000" }},
		{"timeout not less than interval", func(tt *Target) { tt.Timeout = tt.Interval }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := validTarget()
			tc.mutate(&target)
			cfg := Config{Targets: []Target{target}}
			if err := cfg.Validate(); err == nil {
				t.Errorf("Validate() = nil, want error for %s", tc.name)
			}
		})
	}
}
