package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func testConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(&Config{
		Current:  "default",
		Contexts: map[string]Context{"default": {Backend: BackendWebUI}},
	}, Credentials{}); err != nil {
		t.Fatal(err)
	}
}

func TestSetCredentialsPrefersKeyringAndScrubsFile(t *testing.T) {
	keyring.MockInit()
	t.Setenv("WEBTOR_NO_KEYRING", "")
	testConfigDir(t)

	// Simulate a pre-keyring plaintext entry.
	if err := Save(&Config{Current: "default", Contexts: map[string]Context{"default": {Backend: BackendWebUI}}},
		Credentials{"default": {APIKey: "legacy-key"}}); err != nil {
		t.Fatal(err)
	}

	if err := SetCredentials("default", ContextCredentials{APIKey: "fresh-key"}); err != nil {
		t.Fatal(err)
	}
	// The keyring holds the secret, the file copy is scrubbed.
	r, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if r.Creds.APIKey != "fresh-key" {
		t.Errorf("resolved key = %q, want fresh-key", r.Creds.APIKey)
	}
	b, _ := os.ReadFile(filepath.Join(Dir(), "credentials.yaml"))
	if string(b) != "" && string(b) != "{}\n" {
		t.Errorf("credentials.yaml still holds %q, want empty", b)
	}
	if src := CredentialSource("default", Credentials{}); src != "keyring" {
		t.Errorf("source = %q, want keyring", src)
	}

	// Empty credentials delete the keyring entry.
	if err := SetCredentials("default", ContextCredentials{}); err != nil {
		t.Fatal(err)
	}
	if c := keyringGet("default"); c != nil {
		t.Errorf("keyring entry survived logout: %+v", c)
	}
}

func TestNoKeyringFallsBackToFile(t *testing.T) {
	keyring.MockInit()
	t.Setenv("WEBTOR_NO_KEYRING", "1")
	testConfigDir(t)

	if err := SetCredentials("default", ContextCredentials{APIKey: "file-key"}); err != nil {
		t.Fatal(err)
	}
	r, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if r.Creds.APIKey != "file-key" {
		t.Errorf("resolved key = %q, want file-key", r.Creds.APIKey)
	}
	_, creds, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if src := CredentialSource("default", creds); src != "file" {
		t.Errorf("source = %q, want file", src)
	}
}

func TestKeyringUnavailableFallsBackToFile(t *testing.T) {
	// MockInitWithError simulates a headless host with no secret service.
	keyring.MockInitWithError(keyring.ErrNotFound)
	t.Setenv("WEBTOR_NO_KEYRING", "")
	testConfigDir(t)

	if err := SetCredentials("default", ContextCredentials{APIKey: "file-key"}); err != nil {
		t.Fatal(err)
	}
	r, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if r.Creds.APIKey != "file-key" {
		t.Errorf("resolved key = %q, want file-key", r.Creds.APIKey)
	}
}
