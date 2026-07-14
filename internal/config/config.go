package config

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen string    `yaml:"listen"`
	TLS    TLSConfig `yaml:"tls"`
	Auth   Auth      `yaml:"auth"`
	Limits Limits    `yaml:"limits"`
}

func (c Config) PublicBaseURL() string {
	if c.Listen == "" {
		return ""
	}
	scheme := "https"
	if !c.TLS.IsEnabled() {
		scheme = "http"
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return scheme + "://" + c.Listen
	}
	host := "localhost"
	if len(c.TLS.Hosts) > 0 && strings.TrimSpace(c.TLS.Hosts[0]) != "" {
		host = strings.TrimSpace(c.TLS.Hosts[0])
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

type TLSConfig struct {
	Enabled         *bool    `yaml:"enabled"`
	CertificateFile string   `yaml:"certificate_file"`
	PrivateKeyFile  string   `yaml:"private_key_file"`
	AutoGenerate    *bool    `yaml:"auto_generate"`
	Hosts           []string `yaml:"hosts"`
}

type Auth struct {
	TokenHashFile    string `yaml:"token_hash_file"`
	InitialTokenFile string `yaml:"initial_token_file"`
}

type Limits struct {
	MaxConcurrentCommands     int           `yaml:"max_concurrent_commands"`
	MaxFileSize               int64         `yaml:"max_file_size"`
	MaxTimeout                time.Duration `yaml:"-"`
	TransferTicketTTL         time.Duration `yaml:"-"`
	RequestBodyTimeout        time.Duration `yaml:"-"`
	MaxTimeoutSeconds         int64         `yaml:"max_timeout_seconds"`
	TransferTTLSeconds        int64         `yaml:"transfer_ticket_ttl_seconds"`
	RequestBodyTimeoutSeconds int64         `yaml:"http_read_timeout_seconds"`
}

func Load(path string) (Config, error) {
	cfg := defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, fmt.Errorf("config must contain exactly one YAML document")
	}
	if cfg.Limits.MaxTimeoutSeconds <= 0 {
		return Config{}, fmt.Errorf("max_timeout_seconds must be positive")
	}
	if cfg.Limits.TransferTTLSeconds <= 0 {
		return Config{}, fmt.Errorf("transfer_ticket_ttl_seconds must be positive")
	}
	if cfg.Limits.RequestBodyTimeoutSeconds <= 0 {
		return Config{}, fmt.Errorf("http_read_timeout_seconds must be positive")
	}
	cfg.Limits.MaxTimeout = time.Duration(cfg.Limits.MaxTimeoutSeconds) * time.Second
	cfg.Limits.TransferTicketTTL = time.Duration(cfg.Limits.TransferTTLSeconds) * time.Second
	cfg.Limits.RequestBodyTimeout = time.Duration(cfg.Limits.RequestBodyTimeoutSeconds) * time.Second
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Limits.MaxConcurrentCommands <= 0 {
		return fmt.Errorf("max_concurrent_commands must be positive")
	}
	if c.Limits.MaxFileSize <= 0 {
		return fmt.Errorf("max_file_size must be positive")
	}
	if c.Limits.MaxTimeout <= 0 {
		return fmt.Errorf("max_timeout_seconds must be positive")
	}
	if c.Limits.TransferTicketTTL <= 0 {
		return fmt.Errorf("transfer_ticket_ttl_seconds must be positive")
	}
	if c.Limits.RequestBodyTimeout <= 0 {
		return fmt.Errorf("http_read_timeout_seconds must be positive")
	}
	return nil
}

func (c TLSConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

func (c TLSConfig) AutoGenerateEnabled() bool {
	return c.AutoGenerate == nil || *c.AutoGenerate
}

func defaults() Config {
	return Config{
		Listen: "0.0.0.0:9443",
		TLS: TLSConfig{
			CertificateFile: "/etc/deploymate/tls/server.crt",
			PrivateKeyFile:  "/etc/deploymate/tls/server.key",
		},
		Auth: Auth{
			TokenHashFile:    "/etc/deploymate/token.sha256",
			InitialTokenFile: "/etc/deploymate/initial-token",
		},
		Limits: Limits{
			MaxConcurrentCommands:     4,
			MaxFileSize:               100 * 1024 * 1024,
			MaxTimeout:                24 * time.Hour,
			TransferTicketTTL:         10 * time.Minute,
			RequestBodyTimeout:        30 * time.Minute,
			MaxTimeoutSeconds:         86400,
			TransferTTLSeconds:        600,
			RequestBodyTimeoutSeconds: 1800,
		},
	}
}
