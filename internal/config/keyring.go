package config

import (
	"encoding/json"
	"os"

	"github.com/zalando/go-keyring"
)

// Secrets live in the OS keyring (macOS Keychain, Windows Credential
// Manager, Linux Secret Service) whenever one is available; credentials.yaml
// (0600) is the fallback for headless environments and the read-path legacy
// for keys stored before the keyring existed. WEBTOR_NO_KEYRING=1 disables
// the keyring outright — the test suite sets it so runs never touch the
// developer's real keychain.

const keyringService = "webtor-cli"

func keyringEnabled() bool { return os.Getenv("WEBTOR_NO_KEYRING") == "" }

// keyringGet returns the context's secrets from the OS keyring, or nil when
// the keyring is disabled, unavailable, or has no entry.
func keyringGet(name string) *ContextCredentials {
	if !keyringEnabled() {
		return nil
	}
	v, err := keyring.Get(keyringService, name)
	if err != nil {
		return nil
	}
	var c ContextCredentials
	if err := json.Unmarshal([]byte(v), &c); err != nil {
		return nil
	}
	return &c
}

// keyringSet stores the context's secrets, reporting whether the keyring
// actually took them (false → the caller falls back to the file).
func keyringSet(name string, c ContextCredentials) bool {
	if !keyringEnabled() {
		return false
	}
	b, err := json.Marshal(c)
	if err != nil {
		return false
	}
	return keyring.Set(keyringService, name, string(b)) == nil
}

// keyringDelete removes the context's entry; missing entries are fine.
func keyringDelete(name string) {
	if !keyringEnabled() {
		return
	}
	_ = keyring.Delete(keyringService, name)
}
