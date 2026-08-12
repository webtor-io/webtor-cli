package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
)

func playCmd() *cli.Command {
	return &cli.Command{
		Name:      "play",
		Aliases:   []string{"p"},
		Usage:     "stream a file straight into a media player (VLC by default)",
		ArgsUsage: "<resource-id | magnet> [CONTENT-ID | PATH]",
		Description: "Resolves the file's download URL and hands it to the player. Without a\n" +
			"content argument the biggest video (or audio) file is picked. A full magnet\n" +
			"link that is not in the store yet is added first, so\n\n" +
			"   webtor play \"magnet:?xt=urn:btih:...\"\n\n" +
			"is a one-step way from a magnet to a playing video.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "player", Value: "vlc", Usage: "player command to launch (vlc, mpv, …)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// One stdin read serves both: the id for lookup and, when the
			// payload is a full magnet, the add-on-miss fallback below.
			arg, rest, err := resourceAndRest(cmd, true)
			if err != nil {
				return err
			}
			rid := extractResourceID(arg)
			c, _, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}

			res, err := c.Resource(ctx, rid)
			if webtor.IsNotFound(err) && strings.HasPrefix(arg, "magnet:") {
				if !cmd.Bool("quiet") && !cmd.Bool("json") {
					_, _ = fmt.Fprintln(os.Stderr, "not stored yet — adding the magnet first (a cold one can take a few minutes)…")
				}
				res, err = c.AddResource(ctx, webtor.Magnet(arg))
			}
			if err != nil {
				return err
			}

			var contentArg string
			if len(rest) > 0 {
				contentArg = rest[0]
			}
			return runPlay(ctx, cmd, c, res, contentArg)
		},
	}
}

// runPlay resolves the file (asking interactively on a TTY when the torrent
// offers several) and launches the player. Shared with the interactive
// library browser.
func runPlay(ctx context.Context, cmd *cli.Command, c *webtor.Client, res *webtor.ResourceResponse, contentArg string) error {
	item, resp, err := pickPlayable(ctx, cmd, c, res, contentArg)
	if err != nil {
		return err
	}
	u, err := downloadURLFor(ctx, c, res.ID, item, resp)
	if err != nil {
		return err
	}
	player := cmd.String("player")
	if player == "" {
		player = "vlc"
	}
	if err := launchPlayer(player, u); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return exitcode.Usagef("cannot launch %s: %v — install it or pass --player", player, err)
		}
		return fmt.Errorf("launching %s: %w", player, err)
	}
	if cmd.Bool("json") {
		return render.JSON(os.Stdout, map[string]string{
			"player": player, "file": item.Path, "url": u,
		})
	}
	if !cmd.Bool("quiet") {
		_, _ = fmt.Fprintf(os.Stderr, "launched %s: %s\n", player, strings.TrimPrefix(item.Path, "/"))
	}
	return nil
}

func playable(f webtor.MediaFormat) bool {
	return f == webtor.MediaFormatVideo || f == webtor.MediaFormatAudio
}

// pickPlayable selects the file to play: the explicit argument (which must
// name a file), the single file of a single-file torrent (which must be
// media), and otherwise the torrent's media files — interactively on a TTY
// when there are several (Enter takes the biggest video, preserving the
// non-interactive default), the biggest video/audio when scripted. The
// ExportResponse is non-nil when selection already resolved one.
func pickPlayable(ctx context.Context, cmd *cli.Command, c *webtor.Client, res *webtor.ResourceResponse, arg string) (*webtor.ListItem, *webtor.ExportResponse, error) {
	if arg != "" {
		item, resp, err := resolveContent(ctx, c, res.ID, arg)
		if err != nil {
			return nil, nil, err
		}
		if item.Type == webtor.ListTypeDirectory {
			return nil, nil, exitcode.Usagef("%q is a directory — pick a file (see `webtor ls %s %s`)", arg, res.ID, item.Path)
		}
		return item, resp, nil
	}
	if res.File != nil {
		if !playable(res.File.MediaFormat) {
			return nil, nil, exitcode.Usagef("%q is not a playable media file", strings.TrimPrefix(res.File.Path, "/"))
		}
		return res.File, nil, nil
	}
	// The file count is known, so the flat listing fits one request (the
	// server caps a page at 1000).
	limit := min(max(res.FilesCount, 1), 1000)
	var media []webtor.ListItem
	best := -1 // biggest video, falling back to biggest audio
	for it, err := range c.ListAll(ctx, res.ID, webtor.ListOptions{Output: webtor.ListOutputFlat, Limit: limit}) {
		if err != nil {
			return nil, nil, err
		}
		if !playable(it.MediaFormat) {
			continue
		}
		media = append(media, it)
		i := len(media) - 1
		if best == -1 ||
			(it.MediaFormat == webtor.MediaFormatVideo && media[best].MediaFormat != webtor.MediaFormatVideo) ||
			(it.MediaFormat == media[best].MediaFormat && it.Size > media[best].Size) {
			best = i
		}
	}
	switch {
	case len(media) == 0:
		return nil, nil, exitcode.Usagef("no playable media in this torrent — pick a file explicitly (see `webtor ls %s`)", res.ID)
	case len(media) == 1:
		return &media[0], nil, nil
	case interactive(cmd):
		items := make([]picker.Item, len(media))
		for i, it := range media {
			items[i] = picker.Item{Label: strings.TrimPrefix(it.Path, "/"), Detail: render.Size(it.Size)}
		}
		n, err := picker.Pick("Which file?", items, best)
		if err != nil {
			return nil, nil, err
		}
		return &media[n], nil, nil
	default:
		return &media[best], nil, nil
	}
}

// interactive reports whether prompting the person is appropriate: a real
// terminal on both ends and no machine-output flags.
func interactive(cmd *cli.Command) bool {
	return render.IsTTY(os.Stdin) && render.IsTTY(os.Stderr) &&
		!cmd.Bool("quiet") && !cmd.Bool("json")
}

// launchPlayer starts the player detached. The command name is looked up on
// PATH first; on macOS an app-bundle fallback (`open -a`) covers players
// installed as .app without a CLI shim. A missing player wraps
// exec.ErrNotFound so the caller can tell "install it" apart from a real
// launch failure. There is deliberately no `cmd /c start` fallback on
// Windows: cmd.exe splits the signed URL at every `&` and executes the
// pieces — players there must be on PATH.
func launchPlayer(player, url string) error {
	if p, err := exec.LookPath(player); err == nil {
		return exec.Command(p, url).Start()
	}
	if runtime.GOOS == "darwin" {
		app := map[string]string{"vlc": "VLC", "iina": "IINA"}[strings.ToLower(player)]
		if app == "" {
			app = player
		}
		if err := exec.Command("open", "-a", app, url).Run(); err != nil {
			return fmt.Errorf("no %q app bundle: %w", app, exec.ErrNotFound)
		}
		return nil
	}
	return fmt.Errorf("%s: %w on PATH", player, exec.ErrNotFound)
}
