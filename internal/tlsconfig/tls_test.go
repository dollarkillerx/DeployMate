package tlsconfig

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCertificateGeneratesAndReusesFiles(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")

	first, err := EnsureCertificate(certPath, keyPath, []string{"127.0.0.1", "deploymate.local"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Fingerprint == "" {
		t.Fatalf("unexpected certificate result: %+v", first)
	}
	certBefore, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}

	second, err := EnsureCertificate(certPath, keyPath, []string{"ignored.example"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.Fingerprint != first.Fingerprint {
		t.Fatalf("certificate was not reused: first=%+v second=%+v", first, second)
	}
	certAfter, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(certAfter) != string(certBefore) {
		t.Fatal("existing certificate changed")
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o", keyInfo.Mode().Perm())
	}
}

func TestGeneratedCertificateSupportsInsecureTLSClient(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if _, err := EnsureCertificate(certPath, keyPath, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // #nosec G402: this test verifies the explicitly supported insecure client mode.
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
}
