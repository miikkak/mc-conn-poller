package cmd

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// resetConfigState gives a test a clean viper (with the flag bindings that
// viper.Reset() would otherwise drop) and restores that state afterwards.
func resetConfigState(t *testing.T) {
	t.Helper()
	reset := func() {
		viper.Reset()
		bindFlags()
		cfgFile, configReadErr = "", nil
	}
	reset()
	t.Cleanup(reset)
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitConfigAcceptsNonYAMLExtension(t *testing.T) {
	resetConfigState(t)
	cfgFile = writeFile(t, t.TempDir(), "mc-conn-poller.conf", "log_level: debug\n")

	initConfig()

	if configReadErr != nil {
		t.Fatalf("initConfig() read error = %v, want nil", configReadErr)
	}
	if got := viper.GetString("log_level"); got != "debug" {
		t.Errorf("log_level = %q, want %q", got, "debug")
	}
}

func TestInitConfigFindsFileOnDefaultSearchPath(t *testing.T) {
	resetConfigState(t)
	dir := t.TempDir()
	writeFile(t, dir, "mc-conn-poller.yaml", "log_level: warn\n")
	orig := configSearchPaths
	configSearchPaths = []string{t.TempDir(), dir} // the first has no config
	t.Cleanup(func() { configSearchPaths = orig })

	initConfig()

	if configReadErr != nil {
		t.Fatalf("initConfig() read error = %v, want nil", configReadErr)
	}
	if got := viper.GetString("log_level"); got != "warn" {
		t.Errorf("log_level = %q, want %q", got, "warn")
	}
}

func TestInitConfigMissingFileIsNotAnError(t *testing.T) {
	resetConfigState(t)
	orig := configSearchPaths
	configSearchPaths = []string{t.TempDir()}
	t.Cleanup(func() { configSearchPaths = orig })

	initConfig()

	if configReadErr != nil {
		t.Errorf("initConfig() read error = %v, want nil for a missing default config", configReadErr)
	}
}

func TestInitConfigReportsUnparsableFile(t *testing.T) {
	resetConfigState(t)
	cfgFile = writeFile(t, t.TempDir(), "mc-conn-poller.yaml", "targets: [\n")

	initConfig()

	if configReadErr == nil {
		t.Error("initConfig() read error = nil, want the YAML syntax error")
	}
}

func TestNewLoggerRejectsInvalidLevel(t *testing.T) {
	if _, err := newLogger("loud", false); err == nil || !strings.Contains(err.Error(), `invalid log level "loud"`) {
		t.Errorf("newLogger(\"loud\") error = %v, want an invalid log level error", err)
	}
}

func TestNewLoggerStderrLevels(t *testing.T) {
	tests := []struct {
		level   string
		enabled map[slog.Level]bool
	}{
		{"debug", map[slog.Level]bool{slog.LevelDebug: true, slog.LevelInfo: true, slog.LevelError: true}},
		{"info", map[slog.Level]bool{slog.LevelDebug: false, slog.LevelInfo: true, slog.LevelError: true}},
		{"warn", map[slog.Level]bool{slog.LevelInfo: false, slog.LevelWarn: true, slog.LevelError: true}},
		{"error", map[slog.Level]bool{slog.LevelWarn: false, slog.LevelError: true}},
	}
	for _, tc := range tests {
		t.Run(tc.level, func(t *testing.T) {
			logger, err := newLogger(tc.level, false)
			if err != nil {
				t.Fatalf("newLogger(%q) = %v, want nil", tc.level, err)
			}
			for lvl, want := range tc.enabled {
				if got := logger.Enabled(context.Background(), lvl); got != want {
					t.Errorf("Enabled(%v) = %v, want %v", lvl, got, want)
				}
			}
		})
	}
}

// runRoot executes the root command against a config file with the given
// content and returns its error. ctx controls how long a successful run lasts.
func runRoot(t *testing.T, ctx context.Context, yaml string) error {
	t.Helper()
	resetConfigState(t)

	prevLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	path := writeFile(t, t.TempDir(), "mc-conn-poller.yaml", yaml)
	RootCmd.SetArgs([]string{"--config", path})
	t.Cleanup(func() { RootCmd.SetArgs(nil) })

	return RootCmd.ExecuteContext(ctx)
}

const validTargetYAML = `targets:
  - name: t
    protocol: java
    host: 127.0.0.1
    port: 1
    ping_url: http://127.0.0.1:1/ping
`

func TestRunEConfigErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"unparsable YAML", "targets: [\n", "reading configuration"},
		{"no targets", "log_level: info\n", "invalid configuration: at least one target must be configured"},
		{"invalid target", "targets:\n  - name: bad\n    protocol: http\n", `invalid configuration: target "bad"`},
		{"invalid log level", validTargetYAML + "log_level: loud\n", `invalid log level "loud"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := runRoot(t, context.Background(), tc.yaml)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestRunERunsAndStopsOnContextCancel(t *testing.T) {
	// The context is already canceled, so poller.Run does its immediate first
	// probe round (aborted, nothing is reported) and returns without waiting
	// on any timer.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runRoot(t, ctx, validTargetYAML); err != nil {
		t.Errorf("RootCmd.ExecuteContext() = %v, want nil on shutdown", err)
	}
}

func TestExecuteReturnsConfigError(t *testing.T) {
	resetConfigState(t)
	path := writeFile(t, t.TempDir(), "mc-conn-poller.yaml", "log_level: info\n")
	RootCmd.SetArgs([]string{"--config", path})
	t.Cleanup(func() { RootCmd.SetArgs(nil) })

	if err := Execute(); err == nil || !strings.Contains(err.Error(), "at least one target") {
		t.Errorf("Execute() = %v, want the no-targets error", err)
	}
}
