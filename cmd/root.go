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
		},
		Commands: []*cli.Command{
			authCmd(),
			addCmd(),
			infoCmd(),
			lsCmd(),
			downloadCmd(),
			exportCmd(),
			streamURLCmd(),
			configCmd(),
		},
	}
}

// Main runs the CLI and returns the process exit code.
func Main(ctx context.Context, args []string) int {
	root := Root()
	err := root.Run(ctx, args)
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
	b, err := r.Backend()
	if err != nil {
		return nil, nil, &exitcode.UsageError{Msg: err.Error()}
	}
	c, err := webtor.New(b, webtor.WithUserAgent("webtor-cli/"+Version))
	if err != nil {
		return nil, nil, err
	}
	return c, r, nil
}
