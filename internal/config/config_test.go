package config

import (
	"strings"
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

		PingTimeout: 5 * time.Second,
		PingURL:     "https://hc-ping.com/00000000-0000-0000-0000-000000000000",
	}
}

func TestValidateAcceptsSingleAttemptBedrockWithinInterval(t *testing.T) {
	target := validTarget()
	target.Protocol = ProtocolBedrock
	target.Transport = "raknet"
	target.Interval = 10 * time.Second
	target.Timeout = 4 * time.Second
	cfg := Config{Targets: []Target{target}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil (4s + 5s ping < 10s)", err)
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
		{"no ping_url or kuma_push_url", func(tt *Target) { tt.PingURL = "" }},
		{"relative kuma_push_url", func(tt *Target) { tt.KumaPushURL = "/not-absolute" }},
		{"non-http(s) kuma_push_url", func(tt *Target) { tt.KumaPushURL = "ftp://kuma.example.com/api/push/abc" }},
		{"relative ping_url", func(tt *Target) { tt.PingURL = "/not-absolute" }},
		{"non-http(s) ping_url", func(tt *Target) { tt.PingURL = "ftp://hc-ping.com/00000000-0000-0000-0000-000000000000" }},
		{"unknown transport", func(tt *Target) { tt.Transport = "quic" }},
		{"unknown signaling_scheme", func(tt *Target) { tt.SignalingScheme = "ftp" }},
		{"unknown ip_family", func(tt *Target) { tt.IPFamily = "both" }},
		{"non-positive ping_timeout", func(tt *Target) { tt.PingTimeout = 0 }},
		{"timeout not less than interval", func(tt *Target) { tt.Timeout = tt.Interval }},
		{"timeout plus ping_timeout not less than interval", func(tt *Target) {
			tt.Interval = 10 * time.Second
			tt.Timeout = 8 * time.Second
		}},
		{"bedrock auto doubles timeout", func(tt *Target) {
			tt.Protocol = ProtocolBedrock
			tt.Interval = 10 * time.Second
			tt.Timeout = 3 * time.Second
		}},
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

func TestValidateDoesNotEchoPingURL(t *testing.T) {
	const secret = "SECRET-PING-TOKEN"

	target := validTarget()
	target.PingURL = "ftp://hc-ping.com/" + secret
	err := Config{Targets: []Target{target}}.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error for non-http(s) ping_url")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error %q leaks the ping URL", err)
	}
}

func TestValidateDoesNotEchoKumaPushURL(t *testing.T) {
	const secret = "SECRET-PUSH-TOKEN"

	target := validTarget()
	target.KumaPushURL = "ftp://kuma.example.com/api/push/" + secret
	err := Config{Targets: []Target{target}}.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error for non-http(s) kuma_push_url")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error %q leaks the push URL", err)
	}
}

func TestValidateAcceptsNotifierCombinations(t *testing.T) {
	const kuma = "https://kuma.example.com/api/push/abc"

	tests := []struct {
		name string
		hc   string
		kuma string
	}{
		{"healthchecks only", "https://hc-ping.com/00000000-0000-0000-0000-000000000000", ""},
		{"kuma only", "", kuma},
		{"both", "https://hc-ping.com/00000000-0000-0000-0000-000000000000", kuma},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := validTarget()
			target.PingURL, target.KumaPushURL = tc.hc, tc.kuma
			if err := (Config{Targets: []Target{target}}).Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestValidateAcceptsIPFamilies(t *testing.T) {
	for _, family := range []string{"", IPFamilyIPv4, IPFamilyIPv6, IPFamilyAlternate} {
		t.Run("ip_family="+family, func(t *testing.T) {
			target := validTarget()
			target.IPFamily = family
			if err := (Config{Targets: []Target{target}}).Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestValidateKumaReportFailure(t *testing.T) {
	const kuma = "https://kuma.example.com/api/push/abc"

	t.Run("requires kuma_push_url", func(t *testing.T) {
		target := validTarget() // has ping_url only
		target.KumaReportFailure = true
		if err := (Config{Targets: []Target{target}}).Validate(); err == nil {
			t.Error("Validate() = nil, want error for kuma_report_failure without kuma_push_url")
		}
	})
	t.Run("accepted with kuma_push_url", func(t *testing.T) {
		target := validTarget()
		target.KumaPushURL = kuma
		target.KumaReportFailure = true
		if err := (Config{Targets: []Target{target}}).Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})
}
