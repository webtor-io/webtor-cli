package cmd

import (
	"context"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/urfave/cli/v3"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
)

var btihRe = regexp.MustCompile(`(?i)urn:btih:([0-9a-f]{40})`)

// resourceAndRest splits the positional arguments into the resource token
// and the rest. When the first argument is not an infohash/magnet and stdin
// is piped, the resource is read from stdin and every positional argument is
// "rest" — so ids compose in pipelines:
//
//	echo 08ada5… | webtor play
//	some-tool | webtor download --stdout Sintel.mp4
//
// (A "-" placeholder was rejected: urfave/cli drops everything after a bare
// dash, so it cannot carry trailing arguments.)
func resourceAndRest(cmd *cli.Command) (raw string, rest []string, err error) {
	s := cmd.Args().Slice()
	if len(s) > 0 && magnetOrHashRe.MatchString(s[0]) {
		return s[0], s[1:], nil
	}
	if !render.IsTTY(os.Stdin) {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return "", nil, err
		}
		if raw = strings.TrimSpace(string(b)); raw != "" {
			return raw, s, nil
		}
	}
	if len(s) > 0 {
		// Not id-shaped, but it is all we have — let the API answer.
		return s[0], s[1:], nil
	}
	return "", nil, exitcode.Usagef("missing <resource-id> argument (pass it, or pipe an infohash/magnet to stdin)")
}

// extractResourceID lowercases a bare infohash or pulls it out of a magnet.
func extractResourceID(raw string) string {
	if m := btihRe.FindStringSubmatch(raw); m != nil {
		return strings.ToLower(m[1])
	}
	return strings.ToLower(raw)
}

// resourceIDArg is the single-argument form of resourceAndRest for commands
// whose only positional argument is the resource.
func resourceIDArg(cmd *cli.Command, pos int) (string, error) {
	if pos > 0 {
		arg := cmd.Args().Get(pos)
		if arg == "" {
			return "", exitcode.Usagef("missing <resource-id> argument")
		}
		return extractResourceID(arg), nil
	}
	raw, _, err := resourceAndRest(cmd)
	if err != nil {
		return "", err
	}
	return extractResourceID(raw), nil
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
