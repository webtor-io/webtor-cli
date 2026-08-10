// Package config loads and saves the CLI's configuration: named contexts in
// config.yaml (shareable) and secrets in credentials.yaml (0600), both under
// $XDG_CONFIG_HOME/webtor (~/.config/webtor by default). The WEBTOR_* env
// vars override everything, so CI can run without any files.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	webtor "github.com/webtor-io/api-sdk-go"
	"gopkg.in/yaml.v3"
)

// BackendName is the config-file spelling of a backend kind.
type BackendName string

const (
	BackendWebUI    BackendName = "webui"
	BackendRapidAPI BackendName = "rapidapi"
	BackendDirect   BackendName = "direct"
)

// Context is one named backend configuration.
type Context struct {
	Backend BackendName `yaml:"backend"`
	// BaseURL overrides the backend's default endpoint. Required for direct.
	BaseURL string `yaml:"base_url,omitempty"`
}

// Config is config.yaml: the contexts and which one is current.
type Config struct {
	Current  string             `yaml:"current"`
	Contexts map[string]Context `yaml:"contexts"`
}

// Credentials is credentials.yaml: per-context secrets, kept out of the
// shareable config file.
type Credentials map[string]*ContextCredentials

// ContextCredentials are the secrets of one context.
type ContextCredentials struct {
	// APIKey is the web-ui API key, the RapidAPI key, or direct's
	// pass-through api-key, depending on the context's backend.
	APIKey string `yaml:"api_key,omitempty"`
	// Token is direct's pass-through JWT.
	Token string `yaml:"token,omitempty"`
}

// Dir returns the configuration directory.
func Dir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "webtor")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".webtor"
	}
	return filepath.Join(home, ".config", "webtor")
}

func configPath() string      { return filepath.Join(Dir(), "config.yaml") }
func credentialsPath() string { return filepath.Join(Dir(), "credentials.yaml") }

// Exists reports whether a config file is present.
func Exists() bool {
	_, err := os.Stat(configPath())
	return err == nil
}

// Load reads config.yaml and credentials.yaml. A missing config is an error;
// missing credentials are not (an empty set is returned).
func Load() (*Config, Credentials, error) {
	b, err := os.ReadFile(configPath())
	if err != nil {
		return nil, nil, fmt.Errorf("no configuration at %s — run `webtor config init` (or set WEBTOR_BACKEND)", configPath())
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", configPath(), err)
	}
	creds := Credentials{}
	if cb, err := os.ReadFile(credentialsPath()); err == nil {
		if err := yaml.Unmarshal(cb, &creds); err != nil {
			return nil, nil, fmt.Errorf("parsing %s: %w", credentialsPath(), err)
		}
	}
	return &cfg, creds, nil
}

// Save writes both files, creating the directory as needed. credentials.yaml
// is written 0600 — it holds keys.
func Save(cfg *Config, creds Credentials) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	cb, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath(), cb, 0o644); err != nil {
		return err
	}
	rb, err := yaml.Marshal(creds)
	if err != nil {
		return err
	}
	return os.WriteFile(credentialsPath(), rb, 0o600)
}

// Resolved is a fully resolved runtime configuration: the chosen context plus
// its secrets, after env overrides.
type Resolved struct {
	Name    string
	Context Context
	Creds   ContextCredentials
}

// Resolve picks the context (flag > config current) and applies WEBTOR_* env
// overrides. When WEBTOR_BACKEND is set, the files are not required at all.
func Resolve(contextFlag string) (*Resolved, error) {
	if b := os.Getenv("WEBTOR_BACKEND"); b != "" {
		r := &Resolved{
			Name:    "env",
			Context: Context{Backend: BackendName(b), BaseURL: os.Getenv("WEBTOR_BASE_URL")},
			Creds: ContextCredentials{
				APIKey: os.Getenv("WEBTOR_API_KEY"),
				Token:  os.Getenv("WEBTOR_TOKEN"),
			},
		}
		if r.Creds.APIKey == "" && r.Context.Backend == BackendRapidAPI {
			r.Creds.APIKey = os.Getenv("WEBTOR_RAPIDAPI_KEY")
		}
		return r, nil
	}
	cfg, creds, err := Load()
	if err != nil {
		return nil, err
	}
	name := contextFlag
	if name == "" {
		name = cfg.Current
	}
	cx, ok := cfg.Contexts[name]
	if !ok {
		return nil, fmt.Errorf("context %q not found in %s", name, configPath())
	}
	r := &Resolved{Name: name, Context: cx}
	if c := creds[name]; c != nil {
		r.Creds = *c
	}
	if k := os.Getenv("WEBTOR_API_KEY"); k != "" {
		r.Creds.APIKey = k
	}
	return r, nil
}

// Backend constructs the SDK backend for the resolved configuration.
func (r *Resolved) Backend() (webtor.Backend, error) {
	switch r.Context.Backend {
	case BackendWebUI:
		var opts []webtor.WebUIOption
		if r.Context.BaseURL != "" {
			opts = append(opts, webtor.WithWebUIBaseURL(r.Context.BaseURL))
		}
		return webtor.WebUI(r.Creds.APIKey, opts...)
	case BackendRapidAPI:
		var opts []webtor.RapidAPIOption
		if r.Context.BaseURL != "" {
			opts = append(opts, webtor.WithRapidAPIBaseURL(r.Context.BaseURL))
		}
		return webtor.RapidAPI(r.Creds.APIKey, opts...)
	case BackendDirect:
		if r.Context.BaseURL == "" {
			return nil, fmt.Errorf("the direct backend needs base_url (or WEBTOR_BASE_URL)")
		}
		var opts []webtor.DirectOption
		if r.Creds.APIKey != "" {
			opts = append(opts, webtor.WithDirectAPIKey(r.Creds.APIKey))
		}
		if r.Creds.Token != "" {
			opts = append(opts, webtor.WithDirectToken(r.Creds.Token))
		}
		return webtor.Direct(r.Context.BaseURL, opts...)
	default:
		return nil, fmt.Errorf("unknown backend %q (want webui, rapidapi or direct)", r.Context.Backend)
	}
}

// SetCredentials updates one context's secrets on disk, preserving the rest.
func SetCredentials(name string, c ContextCredentials) error {
	cfg, creds, err := Load()
	if err != nil {
		return err
	}
	creds[name] = &c
	return Save(cfg, creds)
}
