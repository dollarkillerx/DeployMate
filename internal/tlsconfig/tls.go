package tlsconfig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Result struct {
	Created     bool
	Fingerprint string
	NotAfter    time.Time
}

func EnsureCertificate(certPath, keyPath string, hosts []string) (Result, error) {
	if certPEM, err := os.ReadFile(certPath); err == nil {
		return parseResult(certPEM, false)
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	if len(hosts) == 0 {
		hosts = []string{"localhost", "127.0.0.1"}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Result{}, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return Result{}, err
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hosts[0], Organization: []string{"DeployMate"}},
		NotBefore:    now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, host)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return Result{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Result{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return Result{}, err
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return Result{}, err
	}
	return parseResult(certPEM, true)
}

func parseResult(certPEM []byte, created bool) (Result, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return Result{}, fmt.Errorf("invalid certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(cert.Raw)
	return Result{Created: created, Fingerprint: "sha256:" + hex.EncodeToString(sum[:]), NotAfter: cert.NotAfter}, nil
}
