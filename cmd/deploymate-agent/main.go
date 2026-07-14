package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jianping5/DeployMate/internal/audit"
	"github.com/jianping5/DeployMate/internal/auth"
	commandpkg "github.com/jianping5/DeployMate/internal/command"
	"github.com/jianping5/DeployMate/internal/config"
	filespkg "github.com/jianping5/DeployMate/internal/files"
	jobspkg "github.com/jianping5/DeployMate/internal/jobs"
	"github.com/jianping5/DeployMate/internal/mcpserver"
	"github.com/jianping5/DeployMate/internal/tlsconfig"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	if len(args) > 0 && args[0] == "rotate-token" {
		return rotateToken(args[1:], logger)
	}
	flags := flag.NewFlagSet("deploymate-agent", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/deploymate/agent.yaml", "agent configuration file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.TLS.IsEnabled() && cfg.TLS.AutoGenerateEnabled() {
		result, err := tlsconfig.EnsureCertificate(cfg.TLS.CertificateFile, cfg.TLS.PrivateKeyFile, cfg.TLS.Hosts)
		if err != nil {
			return fmt.Errorf("ensure TLS certificate: %w", err)
		}
		logger.Info("TLS certificate ready", "created", result.Created, "certificate", cfg.TLS.CertificateFile, "fingerprint", result.Fingerprint, "not_after", result.NotAfter)
	}
	_, created, err := auth.EnsureToken(cfg.Auth.TokenHashFile, cfg.Auth.InitialTokenFile)
	if err != nil {
		return fmt.Errorf("ensure token: %w", err)
	}
	if created {
		logger.Warn("initial bearer token created", "path", cfg.Auth.InitialTokenFile)
	}
	verifier, err := auth.LoadVerifier(cfg.Auth.TokenHashFile)
	if err != nil {
		return err
	}
	runner := commandpkg.NewRunnerWithTimeouts(cfg.Limits.MaxConcurrentCommands, 4*1024*1024, 60*time.Second, cfg.Limits.MaxTimeout)
	stateDir := "/var/lib/deploymate"
	jobs, err := jobspkg.NewManager(filepath.Join(stateDir, "jobs"), runner)
	if err != nil {
		return err
	}
	fileManager := filespkg.NewManager(cfg.Limits.MaxFileSize, cfg.Limits.TransferTicketTTL)
	mcpHandler := mcpserver.NewHandler(mcpserver.Dependencies{Runner: runner, Jobs: jobs, Files: fileManager, PublicBaseURL: cfg.PublicBaseURL(), Version: version, Logger: logger})
	mux := http.NewServeMux()
	mux.Handle("/readme", mcpserver.ReadmeHandler(version))
	mux.Handle("/mcp", auth.Middleware(verifier, mcpHandler))
	mux.Handle("/files/", fileManager.Handler())
	server := &http.Server{Addr: cfg.Listen, Handler: audit.Middleware(logger, mux), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: cfg.Limits.RequestBodyTimeout, IdleTimeout: 2 * time.Minute, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	logger.Info("deploymate agent listening", "address", cfg.Listen, "mcp_endpoint", "/mcp", "tls", cfg.TLS.IsEnabled())
	if cfg.TLS.IsEnabled() {
		err = server.ListenAndServeTLS(cfg.TLS.CertificateFile, cfg.TLS.PrivateKeyFile)
	} else {
		logger.Warn("TLS is disabled; serving plain HTTP — the bearer token is transmitted unencrypted, restrict network access to trusted sources")
		err = server.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func rotateToken(args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("rotate-token", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/deploymate/agent.yaml", "agent configuration file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	_, _, err = auth.RotateToken(cfg.Auth.TokenHashFile, cfg.Auth.InitialTokenFile)
	if err == nil {
		logger.Info("bearer token rotated", "token_file", cfg.Auth.InitialTokenFile)
	}
	return err
}
