// Package wizard is the rclone-style first-run dialog: pick a backend, get
// authenticated, write the config. It runs on `webtor config init` and
// automatically when a command needs configuration, stdin is a TTY, and no
// config exists yet.
package wizard

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/config"
)

// IO bundles the wizard's streams so tests can script it.
type IO struct {
	In  io.Reader
	Out io.Writer
	// OpenBrowser opens a URL in the person's browser; nil for the default.
	OpenBrowser func(url string) error
}

func (w *IO) browser(url string) error {
	if w.OpenBrowser != nil {
		return w.OpenBrowser(url)
	}
	if os.Getenv("WEBTOR_NO_BROWSER") != "" {
		return fmt.Errorf("browser disabled by WEBTOR_NO_BROWSER")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func (w *IO) ask(sc *bufio.Scanner, prompt string) (string, error) {
	_, _ = fmt.Fprint(w.Out, prompt)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return strings.TrimSpace(sc.Text()), nil
}

// Run drives the dialog and writes the resulting configuration as the
// context named "default".
func Run(ctx context.Context, w IO) error {
	sc := bufio.NewScanner(w.In)
	_, _ = fmt.Fprintln(w.Out, "Welcome to webtor! Let's set up how the CLI reaches the API.")
	_, _ = fmt.Fprintln(w.Out, "")
	_, _ = fmt.Fprintln(w.Out, "  1) webtor.io account (recommended)")
	_, _ = fmt.Fprintln(w.Out, "  2) RapidAPI subscription")
	_, _ = fmt.Fprintln(w.Out, "  3) Self-hosted rest-api")
	_, _ = fmt.Fprintln(w.Out, "")

	choice, err := w.ask(sc, "Backend [1]: ")
	if err != nil {
		return err
	}

	cx := config.Context{}
	creds := config.ContextCredentials{}

	switch choice {
	case "", "1":
		cx.Backend = config.BackendWebUI
		// WEBTOR_BASE_URL lets init target a staging or self-hosted web-ui;
		// it is persisted into the context so later commands use it too.
		cx.BaseURL = os.Getenv("WEBTOR_BASE_URL")
		key, err := webUIAuth(ctx, w, sc, cx.BaseURL)
		if err != nil {
			return err
		}
		creds.APIKey = key
	case "2":
		cx.Backend = config.BackendRapidAPI
		key, err := w.ask(sc, "Your X-RapidAPI-Key (from https://rapidapi.com/webtor/api/torrents-api): ")
		if err != nil {
			return err
		}
		creds.APIKey = key
	case "3":
		cx.Backend = config.BackendDirect
		base, err := w.ask(sc, "Base URL of your rest-api (e.g. http://localhost:8080): ")
		if err != nil {
			return err
		}
		cx.BaseURL = base
		key, err := w.ask(sc, "Pass-through api-key (optional, Enter to skip): ")
		if err != nil {
			return err
		}
		creds.APIKey = key
	default:
		return fmt.Errorf("unknown choice %q", choice)
	}

	cfg := &config.Config{
		Current:  "default",
		Contexts: map[string]config.Context{"default": cx},
	}
	if err := config.Save(cfg, config.Credentials{"default": &creds}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w.Out, "\nSaved to %s. Try: webtor add <magnet>\n", config.Dir())
	return nil
}

// webUIAuth obtains an API key for the webtor.io backend: device flow by
// default, pasting a key as the fallback. baseURL overrides the endpoint
// (tests, staging). Exported for `webtor auth login`.
func webUIAuth(ctx context.Context, w IO, sc *bufio.Scanner, baseURL string) (string, error) {
	how, err := w.ask(sc, "\n  1) Log in via browser (recommended)\n  2) Paste an API key\nMethod [1]: ")
	if err != nil {
		return "", err
	}
	if how == "2" {
		return w.ask(sc, "API key (from https://webtor.io/profile): ")
	}
	return DeviceLogin(ctx, w, baseURL)
}

// DeviceLogin runs the device authorization flow and returns the key.
func DeviceLogin(ctx context.Context, w IO, baseURL string) (string, error) {
	var opts []webtor.WebUIOption
	if baseURL != "" {
		opts = append(opts, webtor.WithWebUIBaseURL(baseURL))
	}
	backend, err := webtor.WebUI("", opts...)
	if err != nil {
		return "", err
	}
	c, err := webtor.New(backend)
	if err != nil {
		return "", err
	}
	host, _ := os.Hostname()
	name := "webtor-cli"
	if host != "" {
		name += " @ " + host
	}
	da, err := c.StartDeviceAuth(ctx, name)
	if err != nil {
		return "", err
	}
	_, _ = fmt.Fprintf(w.Out, "\nFirst, copy your one-time code: %s\n", da.UserCode)
	_, _ = fmt.Fprintf(w.Out, "Then confirm the device at: %s\n", da.VerificationURIComplete)
	if err := w.browser(da.VerificationURIComplete); err == nil {
		_, _ = fmt.Fprintln(w.Out, "(opened in your browser)")
	}
	_, _ = fmt.Fprint(w.Out, "Waiting for confirmation")
	key, err := c.WaitDeviceToken(ctx, da, func() { _, _ = fmt.Fprint(w.Out, ".") })
	_, _ = fmt.Fprintln(w.Out)
	if err != nil {
		return "", err
	}
	_, _ = fmt.Fprintln(w.Out, "Device authorized.")
	return key, nil
}

// WebUIAuth is the exported entry used by `webtor auth login` on an existing
// webui context.
func WebUIAuth(ctx context.Context, w IO, baseURL string) (string, error) {
	return webUIAuth(ctx, w, bufio.NewScanner(w.In), baseURL)
}
