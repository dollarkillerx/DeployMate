package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Verifier struct{ hash [sha256.Size]byte }

func EnsureToken(hashPath, initialPath string) (string, bool, error) {
	if _, err := os.Stat(hashPath); err == nil {
		return "", false, nil
	} else if !os.IsNotExist(err) {
		return "", false, err
	}
	return RotateToken(hashPath, initialPath)
}

func RotateToken(hashPath, initialPath string) (string, bool, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", false, fmt.Errorf("generate token: %w", err)
	}
	plain := "dm_" + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(plain))
	hashTmp, err := preparePrivate(hashPath, []byte(hex.EncodeToString(hash[:])+"\n"))
	if err != nil {
		return "", false, err
	}
	defer os.Remove(hashTmp)
	plainTmp, err := preparePrivate(initialPath, []byte(plain+"\n"))
	if err != nil {
		return "", false, err
	}
	defer os.Remove(plainTmp)
	oldPlain, oldPlainErr := os.ReadFile(initialPath)
	if err := os.Rename(plainTmp, initialPath); err != nil {
		return "", false, err
	}
	if err := os.Rename(hashTmp, hashPath); err != nil {
		if oldPlainErr == nil {
			_ = writePrivate(initialPath, oldPlain)
		}
		return "", false, err
	}
	return plain, true, nil
}

func LoadVerifier(path string) (*Verifier, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(decoded) != sha256.Size {
		return nil, fmt.Errorf("invalid token hash")
	}
	v := &Verifier{}
	copy(v.hash[:], decoded)
	return v, nil
}

func (v *Verifier) Verify(token string) bool {
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(got[:], v.hash[:]) == 1
}

func writePrivate(path string, data []byte) error {
	tmp, err := preparePrivate(path, data)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, path)
}

func preparePrivate(path string, data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".deploymate-token-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := f.Sync(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	f = nil
	return tmp, nil
}
