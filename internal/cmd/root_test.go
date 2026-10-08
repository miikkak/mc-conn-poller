package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestInitConfigAcceptsNonYAMLExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-conn-poller.conf")
	if err := os.WriteFile(path, []byte("log_level: debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	cfgFile, configReadErr = path, nil
	t.Cleanup(func() {
		viper.Reset()
		cfgFile, configReadErr = "", nil
	})

	initConfig()

	if configReadErr != nil {
		t.Fatalf("initConfig() read error = %v, want nil", configReadErr)
	}
	if got := viper.GetString("log_level"); got != "debug" {
		t.Errorf("log_level = %q, want %q", got, "debug")
	}
}
