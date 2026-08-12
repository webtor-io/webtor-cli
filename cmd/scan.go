package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/config"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
	"github.com/webtor-io/webtor-cli/internal/torrentfile"
)

func scanCmd() *cli.Command {
	return &cli.Command{
		Name:      "scan",
		Usage:     "scan a folder for .torrent files",
		ArgsUsage: "[DIR]",
		Description: "Walks DIR (default: the configured download folder, else the current\n" +
			"directory) for .torrent files and lists them like the library. On a\n" +
			"terminal a picked file is added to the store (idempotent) and opens\n" +
			"the same action menu library entries get.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			dir := cmd.Args().First()
			if dir == "" {
				if r, err := config.Resolve(cmd.String("context")); err == nil && r.DownloadDir != "" {
					dir = r.DownloadDir
				} else {
					dir = "."
				}
			}
			infos, skipped, err := scanDir(dir)
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				return exitcode.Usagef("no .torrent files under %s", dir)
			}
			if skipped > 0 && !cmd.Bool("quiet") && !cmd.Bool("json") {
				_, _ = fmt.Fprintf(os.Stderr, "skipped %d unparsable .torrent file(s)\n", skipped)
			}
			if !interactive(cmd) {
				if cmd.Bool("json") {
					return render.JSON(os.Stdout, infos)
				}
				rows := make([][]string, 0, len(infos))
				for _, ti := range infos {
					rows = append(rows, []string{ti.InfoHash, render.Size(ti.Size),
						fmt.Sprintf("%d", ti.FilesCount), ti.Name, ti.Path})
				}
				render.Table(os.Stdout, []string{"INFOHASH", "SIZE", "FILES", "NAME", "PATH"}, rows)
				return nil
			}
			err = scanBrowse(ctx, cmd, dir, infos)
			if back(err) {
				return nil
			}
			return err
		},
	}
}

// scanDir walks dir for .torrent files, newest first is not promised — the
// list follows directory order. Unparsable files are counted, not fatal.
func scanDir(dir string) ([]*torrentfile.Info, int, error) {
	var infos []*torrentfile.Info
	skipped := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".torrent") {
			return nil
		}
		ti, perr := torrentfile.Parse(path)
		if perr != nil {
			skipped++
			return nil
		}
		infos = append(infos, ti)
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("scanning %s: %w", dir, err)
	}
	return infos, skipped, nil
}

// scanBrowse is the interactive list over the scanned files: a picked one is
// pushed to the store (idempotent — same bytes, same id) and opens the
// shared resource menu.
func scanBrowse(ctx context.Context, cmd *cli.Command, dir string, infos []*torrentfile.Info) error {
	c, _, err := newClient(ctx, cmd)
	if err != nil {
		return err
	}
	last := -1
	for {
		items := make([]picker.Item, len(infos))
		for i, ti := range infos {
			rel := ti.Path
			if r, err := filepath.Rel(dir, ti.Path); err == nil {
				rel = r
			}
			items[i] = picker.Item{Label: ti.Name,
				Detail: fmt.Sprintf("%s, %d files, %s", render.Size(ti.Size), ti.FilesCount, rel)}
		}
		n, err := picker.Pick(fmt.Sprintf("Torrent files in %s:", dir), items, min(last, len(items)-1))
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
		last = n
		ti := infos[n]
		b, err := os.ReadFile(ti.Path)
		if err != nil {
			return err
		}
		res, err := c.AddResource(ctx, webtor.TorrentBytes(b))
		if err != nil {
			return err
		}
		if err := resourceMenu(ctx, cmd, c, res.ID); err != nil && !back(err) {
			return err
		}
	}
}
