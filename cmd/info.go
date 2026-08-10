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
// and the rest, so ids compose in pipelines:
//
//	echo 08ada5… | webtor play
//	some-tool | webtor download --stdout Sintel.mp4
//
// The resource is read from stdin only when it cannot come from the
// arguments: no arguments at all, or — for commands that take content
// arguments (wantsContent) — a first argument that is not id-shaped. A
// single non-id-shaped argument of a single-argument command is passed
// through to the API instead of touching stdin, so a stray pipe (e.g. a
// `while read` loop's shared stdin) is never drained by a malformed id.
//
// (A "-" placeholder was rejected: urfave/cli drops everything after a bare
// dash, so it cannot carry trailing arguments.)
func resourceAndRest(cmd *cli.Command, wantsContent bool) (raw string, rest []string, err error) {
	s := cmd.Args().Slice()
	switch {
	case len(s) > 0 && magnetOrHashRe.MatchString(s[0]):
		return s[0], s[1:], nil
	case len(s) == 0 && !render.IsTTY(os.Stdin):
		raw, err = readStdinResource()
		return raw, nil, err
	case len(s) > 0 && wantsContent && !render.IsTTY(os.Stdin):
		raw, err = readStdinResource()
		return raw, s, err
	case len(s) > 0:
		// Not id-shaped, but it is all we have — let the API answer.
		return s[0], s[1:], nil
	}
	return "", nil, exitcode.Usagef("missing <resource-id> argument (pass it, or pipe an infohash/magnet to stdin)")
}

// readStdinResource reads the piped resource token and validates its shape,
// so binary garbage or a multi-line feed becomes a usage error instead of a
// mangled API call.
func readStdinResource() (string, error) {
	const maxStdinResource = 1 << 20
	b, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdinResource+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxStdinResource {
		return "", exitcode.Usagef("stdin is too large to be an infohash or magnet")
	}
	var line string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if line != "" {
			return "", exitcode.Usagef("stdin carries more than one line — pipe a single infohash or magnet")
		}
		line = l
	}
	if line == "" {
		return "", exitcode.Usagef("missing <resource-id> argument (pass it, or pipe an infohash/magnet to stdin)")
	}
	if !magnetOrHashRe.MatchString(line) {
		return "", exitcode.Usagef("stdin does not look like an infohash or magnet")
	}
	return line, nil
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
func resourceIDArg(cmd *cli.Command) (string, error) {
	raw, _, err := resourceAndRest(cmd, false)
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
			rid, err := resourceIDArg(cmd)
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
