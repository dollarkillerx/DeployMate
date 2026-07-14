package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	data := []byte("listen: 127.0.0.1:9443\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits.MaxConcurrentCommands != 4 {
		t.Fatalf("MaxConcurrentCommands = %d", cfg.Limits.MaxConcurrentCommands)
	}
	if cfg.Limits.MaxFileSize != 100*1024*1024 {
		t.Fatalf("MaxFileSize = %d", cfg.Limits.MaxFileSize)
	}
	if cfg.Limits.MaxTimeout != 24*time.Hour {
		t.Fatalf("MaxTimeout = %s", cfg.Limits.MaxTimeout)
	}
	if cfg.Limits.TransferTicketTTL != 10*time.Minute {
		t.Fatalf("TransferTicketTTL = %s", cfg.Limits.TransferTicketTTL)
	}
}

func TestLoadRejectsInvalidLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	data := []byte("limits:\n  max_concurrent_commands: -1\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load succeeded with a negative command limit")
	}
}

func TestLoadRejectsNegativeTimeoutAndUnknownFields(t *testing.T) {
	for name, data := range map[string]string{
		"negative timeout": "limits:\n  max_timeout_seconds: -1\n",
		"unknown field":    "limits:\n  max_concurent_commands: 4\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.yaml")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load accepted invalid configuration")
			}
		})
	}
}

func TestLoadDefaultsRequestBodyTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:9443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits.RequestBodyTimeout != 30*time.Minute {
		t.Fatalf("RequestBodyTimeout=%s", cfg.Limits.RequestBodyTimeout)
	}
}

func TestPublicBaseURLUsesFirstTLSHostAndListenPort(t *testing.T) {
	cfg := defaults()
	cfg.Listen = "0.0.0.0:9555"
	cfg.TLS.Hosts = []string{"203.0.113.10"}
	if got := cfg.PublicBaseURL(); got != "https://203.0.113.10:9555" {
		t.Fatalf("PublicBaseURL() = %q", got)
	}
}

func TestTLSDisabledUsesHTTPScheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	data := []byte("listen: 0.0.0.0:9443\ntls:\n  enabled: false\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS.IsEnabled() {
		t.Fatal("IsEnabled() = true, want false")
	}
	cfg.TLS.Hosts = []string{"203.0.113.10"}
	if got := cfg.PublicBaseURL(); got != "http://203.0.113.10:9443" {
		t.Fatalf("PublicBaseURL() = %q, want http scheme", got)
	}
}

func TestTLSEnabledByDefault(t *testing.T) {
	if !(TLSConfig{}).IsEnabled() {
		t.Fatal("TLS should be enabled when 'enabled' is unset")
	}
}
