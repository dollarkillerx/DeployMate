package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureTokenCreatesAndVerifiesToken(t *testing.T) {
	dir := t.TempDir()
	hashPath := filepath.Join(dir, "token.sha256")
	initialPath := filepath.Join(dir, "initial-token")

	plain, created, err := EnsureToken(hashPath, initialPath)
	if err != nil {
		t.Fatal(err)
	}
	if !created || !strings.HasPrefix(plain, "dm_") {
		t.Fatalf("unexpected token result: created=%v token=%q", created, plain)
	}
	verifier, err := LoadVerifier(hashPath)
	if err != nil {
		t.Fatal(err)
	}
	if !verifier.Verify(plain) || verifier.Verify(plain+"wrong") {
		t.Fatal("token verifier returned an incorrect result")
	}
	info, err := os.Stat(initialPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("initial token mode = %o", info.Mode().Perm())
	}
}

func TestEnsureTokenReusesExistingHash(t *testing.T) {
	dir := t.TempDir()
	hashPath := filepath.Join(dir, "token.sha256")
	initialPath := filepath.Join(dir, "initial-token")
	if _, _, err := EnsureToken(hashPath, initialPath); err != nil {
		t.Fatal(err)
	}
	plain, created, err := EnsureToken(hashPath, initialPath)
	if err != nil {
		t.Fatal(err)
	}
	if created || plain != "" {
		t.Fatalf("existing token was replaced: created=%v token=%q", created, plain)
	}
}

func TestRotateTokenKeepsOldHashWhenPlaintextCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	hashPath := filepath.Join(dir, "token.sha256")
	initialPath := filepath.Join(dir, "initial-token")
	oldToken, _, err := EnsureToken(hashPath, initialPath)
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RotateToken(hashPath, filepath.Join(blocker, "token")); err == nil {
		t.Fatal("RotateToken succeeded with an invalid plaintext path")
	}
	verifier, err := LoadVerifier(hashPath)
	if err != nil {
		t.Fatal(err)
	}
	if !verifier.Verify(oldToken) {
		t.Fatal("failed rotation replaced the active token hash")
	}
}
