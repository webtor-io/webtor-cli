package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
)

var magnetOrHashRe = regexp.MustCompile(`^([0-9a-fA-F]{40}|magnet:.*)$`)

func addCmd() *cli.Command {
	return &cli.Command{
		Name:      "add",
		Usage:     "store a torrent (magnet link, infohash, .torrent file, or stdin)",
		ArgsUsage: "[magnet | infohash | file.torrent | -]",
		Description: "Stores a torrent in the content-addressed store and prints its resource id.\n" +
			"Reads a .torrent from stdin when piped (or when the argument is \"-\"):\n\n" +
			"   cat file.torrent | webtor add\n\n" +
			"A cold magnet can take a few minutes to resolve.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			src, err := resolveSource(cmd)
			if err != nil {
				return err
			}
			c, _, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}
			if !cmd.Bool("quiet") && !cmd.Bool("json") && render.IsTTY(os.Stderr) {
				_, _ = fmt.Fprintln(os.Stderr, "storing torrent (a cold magnet can take a few minutes)…")
			}
			res, err := c.AddResource(ctx, src)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return render.JSON(os.Stdout, res)
			}
			printResource(res)
			return nil
		},
	}
}

// resolveSource picks the torrent source from the argument or stdin.
func resolveSource(cmd *cli.Command) (webtor.ResourceSource, error) {
	arg := cmd.Args().First()
	switch {
	case arg == "" || arg == "-":
		if arg == "" && render.IsTTY(os.Stdin) {
			return webtor.ResourceSource{}, exitcode.Usagef(
				"nothing to add: pass a magnet link, an infohash, a .torrent file, or pipe one to stdin")
		}
		return sniffStdin(os.Stdin)
	case magnetOrHashRe.MatchString(arg):
		return webtor.Magnet(arg), nil
	default:
		b, err := os.ReadFile(arg)
		if err != nil {
			return webtor.ResourceSource{}, exitcode.Usagef("cannot read %s: %v", arg, err)
		}
		return webtor.TorrentBytes(b), nil
	}
}

// sniffStdin reads the piped payload and tells a magnet link (or bare
// infohash) from raw .torrent bytes, so `echo "magnet:..." | webtor add`
// works the same as piping a file. Text payloads are trimmed — a trailing
// newline from echo must not reach the magnet parser.
func sniffStdin(r io.Reader) (webtor.ResourceSource, error) {
	b, err := io.ReadAll(io.LimitReader(r, webtor.MaxTorrentSize+1))
	if err != nil {
		return webtor.ResourceSource{}, err
	}
	if len(b) < 4096 {
		if s := strings.TrimSpace(string(b)); magnetOrHashRe.MatchString(s) {
			return webtor.Magnet(s), nil
		}
	}
	return webtor.TorrentBytes(b), nil
}

func printResource(res *webtor.ResourceResponse) {
	rows := [][]string{
		{"id", res.ID},
		{"name", res.Name},
		{"size", render.Size(res.Size)},
		{"files", fmt.Sprintf("%d", res.FilesCount)},
	}
	if res.File != nil {
		rows = append(rows, []string{"file", strings.TrimPrefix(res.File.Path, "/")})
	}
	render.Table(os.Stdout, nil, rows)
}
