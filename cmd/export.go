package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
)

func exportCmd() *cli.Command {
	return &cli.Command{
		Name:      "export",
		Usage:     "resolve the short-lived export URLs of a file",
		ArgsUsage: "<resource-id> <content-id | path>",
		Description: "Prints every export the file has (download, stream, subtitles, …).\n" +
			"The URLs are short-lived and self-authorizing: use them right away,\n" +
			"never store them.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "types", Usage: "comma-separated subset: download,stream,torrent_client_stat,subtitles,media_probe"},
			&cli.StringFlag{Name: "imdb-id", Usage: "IMDB id to improve subtitle lookup"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			raw, rest, err := resourceAndRest(cmd, true)
			if err != nil {
				return err
			}
			rid := extractResourceID(raw)
			if len(rest) == 0 {
				return exitcode.Usagef("missing <content-id> argument (see `webtor ls %s`)", rid)
			}
			c, _, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}
			item, _, err := resolveContent(ctx, c, rid, rest[0])
			if err != nil {
				return err
			}
			o := webtor.ExportOptions{IMDBID: cmd.String("imdb-id")}
			for _, t := range strings.Split(cmd.String("types"), ",") {
				if t = strings.TrimSpace(t); t != "" {
					o.Types = append(o.Types, webtor.ExportType(t))
				}
			}
			resp, err := c.Export(ctx, rid, item.ID, o)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return render.JSON(os.Stdout, resp)
			}
			var rows [][]string
			for _, t := range []webtor.ExportType{
				webtor.ExportTypeDownload, webtor.ExportTypeStream, webtor.ExportTypeSubtitles,
				webtor.ExportTypeMediaProbe, webtor.ExportTypeTorrentClientStat,
			} {
				if e, ok := resp.Exports[t]; ok && e.URL != "" {
					rows = append(rows, []string{string(t), e.URL})
				}
			}
			render.Table(os.Stdout, []string{"TYPE", "URL"}, rows)
			if resp.Cached() {
				_, _ = fmt.Fprintln(os.Stderr, "content is cached — transfers start instantly")
			}
			return nil
		},
	}
}

func urlCmd() *cli.Command {
	return &cli.Command{
		Name:      "url",
		Usage:     "print a file's download URL (short-lived, use right away)",
		ArgsUsage: "<resource-id> <content-id | path>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			raw, rest, err := resourceAndRest(cmd, true)
			if err != nil {
				return err
			}
			rid := extractResourceID(raw)
			if len(rest) == 0 {
				return exitcode.Usagef("missing <content-id> argument (see `webtor ls %s`)", rid)
			}
			c, _, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}
			item, resp, err := resolveContent(ctx, c, rid, rest[0])
			if err != nil {
				return err
			}
			u, err := downloadURLFor(ctx, c, rid, item, resp)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return render.JSON(os.Stdout, map[string]string{"url": u})
			}
			_, _ = fmt.Println(u)
			return nil
		},
	}
}
