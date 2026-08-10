package cmd

import (
	"context"
	"os"
	"regexp"
	"strings"

	"github.com/urfave/cli/v3"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
)

var btihRe = regexp.MustCompile(`(?i)urn:btih:([0-9a-f]{40})`)

// resourceIDArg extracts the resource id from a positional argument that may
// be a bare infohash or a magnet link.
func resourceIDArg(cmd *cli.Command, pos int) (string, error) {
	arg := cmd.Args().Get(pos)
	if arg == "" {
		return "", exitcode.Usagef("missing <resource-id> argument")
	}
	if m := btihRe.FindStringSubmatch(arg); m != nil {
		return strings.ToLower(m[1]), nil
	}
	return strings.ToLower(arg), nil
}

func infoCmd() *cli.Command {
	return &cli.Command{
		Name:      "info",
		Usage:     "show a stored torrent",
		ArgsUsage: "<resource-id | magnet>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			rid, err := resourceIDArg(cmd, 0)
			if err != nil {
				return err
			}
			c, _, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}
			res, err := c.Resource(ctx, rid)
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
