package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/config"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
	"github.com/webtor-io/webtor-cli/internal/wizard"
)

func authCmd() *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "authenticate the CLI",
		Commands: []*cli.Command{
			authLoginCmd(),
			authStatusCmd(),
			authLogoutCmd(),
		},
	}
}

// resolveForAuth loads the current context without triggering the wizard —
// auth commands manage credentials, so a missing key is expected here.
func resolveForAuth(cmd *cli.Command) (*config.Resolved, error) {
	if !config.Exists() && os.Getenv("WEBTOR_BACKEND") == "" {
		return nil, &exitcode.UsageError{Msg: "no configuration yet — run `webtor config init` first"}
	}
	return config.Resolve(cmd.String("context"))
}

func authLoginCmd() *cli.Command {
	return &cli.Command{
		Name:  "login",
		Usage: "obtain and store an API key (device flow by default)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "api-key", Usage: "store this key instead of running the device flow"},
			&cli.BoolFlag{Name: "no-browser", Usage: "print the confirmation URL instead of opening a browser"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			r, err := resolveForAuth(cmd)
			if err != nil {
				return err
			}
			if r.FromEnv {
				return exitcode.Usagef("auth login stores keys into file contexts; in WEBTOR_BACKEND env mode pass the key via WEBTOR_API_KEY instead")
			}
			if r.Context.Backend != config.BackendWebUI {
				return exitcode.Usagef(
					"auth login manages webtor.io account keys; the %s context %q keeps its key in the config — edit it with `webtor config init`",
					r.Context.Backend, r.Name)
			}
			key := cmd.String("api-key")
			if key == "" {
				io := wizard.IO{In: os.Stdin, Out: os.Stderr}
				if cmd.Bool("no-browser") {
					io.OpenBrowser = func(string) error { return errors.New("browser disabled") }
				}
				key, err = wizard.DeviceLogin(ctx, io, r.Context.BaseURL)
				if err != nil {
					return err
				}
			}
			creds := r.Creds
			creds.APIKey = key
			if err := config.SetCredentials(r.Name, creds); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(os.Stderr, "API key saved for context %q.\n", r.Name)
			return probeAfterLogin(ctx, cmd)
		},
	}
}

// probeAfterLogin checks the fresh key against /profile. A payment_required
// answer still means the login worked — the account just is not on a paid
// plan yet — so it must not fail the command.
func probeAfterLogin(ctx context.Context, cmd *cli.Command) error {
	c, _, err := newClient(ctx, cmd)
	if err != nil {
		return err
	}
	p, err := c.Profile(ctx)
	switch {
	case err == nil:
		_, _ = fmt.Fprintf(os.Stderr, "Logged in as %s (%s plan).\n", p.Email, p.Tier.Name)
	case webtor.IsPaymentRequired(err):
		_, _ = fmt.Fprintln(os.Stderr,
			"Logged in. The API needs a paid plan — upgrade at https://webtor.io/donate and the key will start working.")
	default:
		return err
	}
	return nil
}

func authStatusCmd() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "show the active backend and account",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			c, r, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}
			st := map[string]any{
				"context": r.Name,
				"backend": string(r.Context.Backend),
			}
			if r.Context.BaseURL != "" {
				st["base_url"] = r.Context.BaseURL
			}
			if k := r.Creds.APIKey; k != "" {
				st["key"] = mask(k)
			}
			if c.Supports(webtor.CapProfile) {
				p, err := c.Profile(ctx)
				switch {
				case err == nil:
					st["email"] = p.Email
					st["tier"] = p.Tier.Name
					st["scopes"] = p.Scopes
				case webtor.IsPaymentRequired(err):
					st["tier"] = "free (API needs a paid plan)"
				case webtor.IsUnauthorized(err):
					st["tier"] = "not logged in — run `webtor auth login`"
				default:
					return err
				}
			}
			if cmd.Bool("json") {
				return render.JSON(os.Stdout, st)
			}
			order := []string{"context", "backend", "base_url", "key", "email", "tier", "scopes"}
			var rows [][]string
			for _, k := range order {
				if v, ok := st[k]; ok {
					rows = append(rows, []string{k, fmt.Sprintf("%v", v)})
				}
			}
			render.Table(os.Stdout, nil, rows)
			return nil
		},
	}
}

func authLogoutCmd() *cli.Command {
	return &cli.Command{
		Name:  "logout",
		Usage: "forget the stored API key for the active context",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			r, err := resolveForAuth(cmd)
			if err != nil {
				return err
			}
			if r.FromEnv {
				return exitcode.Usagef("nothing is stored in WEBTOR_BACKEND env mode — unset WEBTOR_API_KEY/WEBTOR_TOKEN instead")
			}
			creds := r.Creds
			creds.APIKey = ""
			if err := config.SetCredentials(r.Name, creds); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(os.Stderr, "Key removed for context %q. Revoke the device at https://webtor.io/profile as well.\n", r.Name)
			return nil
		},
	}
}

func mask(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "…" + key[len(key)-4:]
}
