// Package cmd builds the webtor CLI command tree.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/config"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
	"github.com/webtor-io/webtor-cli/internal/wizard"
)

// Version is stamped by the release build via -ldflags.
var Version = "dev"

// Root builds the top-level command.
func Root() *cli.Command {
	return &cli.Command{
		Name:    "webtor",
		Usage:   "stream and download torrents through the webtor API",
		Version: Version,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "machine-readable JSON output"},
			&cli.BoolFlag{Name: "quiet", Aliases: []string{"q"}, Usage: "no progress output"},
			&cli.StringFlag{Name: "context", Usage: "use a named context instead of the current one"},
			&cli.StringFlag{Name: "player", Usage: "player for the interactive menus (default vlc)"},
		},
		Action: rootAction,
		Commands: []*cli.Command{
			authCmd(),
			addCmd(),
			infoCmd(),
			lsCmd(),
			downloadCmd(),
			exportCmd(),
			urlCmd(),
			playCmd(),
			libraryCmd(),
			vaultCmd(),
			scanCmd(),
			profileCmd(),
			configCmd(),
		},
	}
}

// rootAction handles bare `webtor` (the top-level interactive menu on a
// terminal, help otherwise) and `webtor <resource-id|magnet>` (the shared
// resource menu on a terminal, the plain descriptor otherwise).
func rootAction(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()
	switch {
	case len(args) == 0 && interactive(cmd):
		c, _, err := newClient(ctx, cmd)
		if err != nil {
			return err
		}
		err = topMenu(ctx, cmd, c)
		if errors.Is(err, picker.ErrBack) {
			return nil
		}
		return err
	case len(args) >= 1 && magnetOrHashRe.MatchString(args[0]):
		c, _, err := newClient(ctx, cmd)
		if err != nil {
			return err
		}
		rid := extractResourceID(args[0])
		res, err := c.Resource(ctx, rid)
		if webtor.IsNotFound(err) && strings.HasPrefix(args[0], "magnet:") {
			res, err = c.AddResource(ctx, webtor.Magnet(args[0]))
		}
		if err != nil {
			return err
		}
		if !interactive(cmd) {
			if cmd.Bool("json") {
				return render.JSON(os.Stdout, res)
			}
			printResource(res)
			return nil
		}
		err = resourceMenu(ctx, cmd, c, res.ID)
		if errors.Is(err, picker.ErrBack) {
			return nil
		}
		return err
	case len(args) > 0:
		_ = cli.ShowAppHelp(cmd)
		return &exitcode.UsageError{Msg: fmt.Sprintf("unknown command %q", args[0])}
	default:
		return cli.ShowAppHelp(cmd)
	}
}

// Main runs the CLI and returns the process exit code.
func Main(ctx context.Context, args []string) int {
	picker.ExtraHint = downloadsHint
	root := Root()
	err := root.Run(ctx, args)
	// Quitting parks running background downloads as paused — the next
	// session resumes them from the bytes on disk.
	downloads.ParkRunning()
	code, msg := exitcode.Classify(err)
	if code != exitcode.OK && msg != "" {
		if root.Bool("json") {
			jsonCode := "error"
			var ae *webtor.Error
			if errors.As(err, &ae) {
				jsonCode = ae.Code
			}
			render.JSONError(os.Stderr, jsonCode, msg)
		} else {
			_, _ = fmt.Fprintln(os.Stderr, "webtor: "+strings.TrimPrefix(msg, "webtor: "))
		}
	}
	return code
}

// currentCfg is the configuration newClient resolved for this invocation;
// download-path helpers read DownloadDir from it. A CLI process serves one
// invocation, so a package variable is acceptable plumbing here.
var currentCfg *config.Resolved

// currentClient is the SDK client of this invocation — the downloads screen
// resumes paused tasks through it.
var currentClient *webtor.Client

// newClient resolves the configuration (running the first-run wizard when
// appropriate) and builds the SDK client.
func newClient(ctx context.Context, cmd *cli.Command) (*webtor.Client, *config.Resolved, error) {
	r, err := config.Resolve(cmd.String("context"))
	if err != nil {
		if os.Getenv("WEBTOR_BACKEND") == "" && !config.Exists() &&
			render.IsTTY(os.Stdin) && render.IsTTY(os.Stderr) {
			if werr := wizard.Run(ctx, wizard.IO{In: os.Stdin, Out: os.Stderr}); werr != nil {
				return nil, nil, werr
			}
			r, err = config.Resolve(cmd.String("context"))
		}
		if err != nil {
			return nil, nil, &exitcode.UsageError{Msg: err.Error()}
		}
	}
	currentCfg = r
	b, err := r.Backend()
	if err != nil {
		return nil, nil, &exitcode.UsageError{Msg: err.Error()}
	}
	c, err := webtor.New(b, webtor.WithUserAgent("webtor-cli/"+Version))
	if err != nil {
		return nil, nil, err
	}
	currentClient = c
	return c, r, nil
}
