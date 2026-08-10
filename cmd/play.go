package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
)

func playCmd() *cli.Command {
	return &cli.Command{
		Name:      "play",
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
			arg, rest, err := resourceAndRest(cmd)
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
				if !cmd.Bool("quiet") {
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
			item, err := pickPlayable(ctx, c, res, contentArg)
			if err != nil {
				return err
			}
			resp, err := c.Export(ctx, res.ID, item.ID, webtor.ExportOptions{
				Types: []webtor.ExportType{webtor.ExportTypeDownload},
			})
			if err != nil {
				return err
			}
			u, ok := resp.DownloadURL()
			if !ok {
				return &webtor.Error{HTTPStatus: 404, Code: webtor.CodeNotFound,
					Message: fmt.Sprintf("no download export for %q", item.Path)}
			}

			player := cmd.String("player")
			if err := launchPlayer(player, u); err != nil {
				return exitcode.Usagef("cannot launch %s: %v — install it or pass --player", player, err)
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
		},
	}
}

// pickPlayable selects the file to play: the explicit argument, the single
// file of a single-file torrent, or the biggest video (falling back to
// audio) file otherwise.
func pickPlayable(ctx context.Context, c *webtor.Client, res *webtor.ResourceResponse, arg string) (*webtor.ListItem, error) {
	if arg != "" {
		return resolveContent(ctx, c, res.ID, arg)
	}
	if res.File != nil {
		return res.File, nil
	}
	var video, audio *webtor.ListItem
	for it, err := range c.ListAll(ctx, res.ID, webtor.ListOptions{Output: webtor.ListOutputFlat}) {
		if err != nil {
			return nil, err
		}
		switch it.MediaFormat {
		case webtor.MediaFormatVideo:
			if video == nil || it.Size > video.Size {
				v := it
				video = &v
			}
		case webtor.MediaFormatAudio:
			if audio == nil || it.Size > audio.Size {
				a := it
				audio = &a
			}
		}
	}
	if video != nil {
		return video, nil
	}
	if audio != nil {
		return audio, nil
	}
	return nil, exitcode.Usagef("no playable media in this torrent — pick a file explicitly (see `webtor ls %s`)", res.ID)
}

// launchPlayer starts the player detached. The command name is looked up on
// PATH first; on macOS an app-bundle fallback (`open -a`) covers players
// installed as .app without a CLI shim.
func launchPlayer(player, url string) error {
	if p, err := exec.LookPath(player); err == nil {
		return exec.Command(p, url).Start()
	}
	if runtime.GOOS == "darwin" {
		app := map[string]string{"vlc": "VLC", "mpv": "mpv", "iina": "IINA"}[strings.ToLower(player)]
		if app == "" {
			app = player
		}
		return exec.Command("open", "-a", app, url).Run()
	}
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "start", "", player, url).Start()
	}
	return fmt.Errorf("%s not found on PATH", player)
}
