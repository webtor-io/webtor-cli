package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
)

// libraryInteractive is the no-subcommand entry: an interactive browser on a
// terminal, a plain listing when scripted.
func libraryInteractive(ctx context.Context, cmd *cli.Command) error {
	c, _, err := newClient(ctx, cmd)
	if err != nil {
		return err
	}
	if !interactive(cmd) {
		return libraryPrint(ctx, cmd, c)
	}
	err = libraryBrowse(ctx, cmd, c)
	if back(err) {
		return nil
	}
	return err
}

// libraryBrowse lists the library with section and sort toggles (mirroring
// web-ui's Torrents/Movies/Series tabs and the API's sort orders); a picked
// entry opens the shared resourceMenu. Esc goes back to the caller.
func libraryBrowse(ctx context.Context, cmd *cli.Command, c *webtor.Client) error {
	types := []webtor.LibraryType{webtor.LibraryTypeAll, webtor.LibraryTypeMovies, webtor.LibraryTypeSeries}
	sortsFor := func(t webtor.LibraryType) []webtor.LibrarySort {
		if t == webtor.LibraryTypeMovies || t == webtor.LibraryTypeSeries {
			return []webtor.LibrarySort{webtor.LibrarySortRecent, webtor.LibrarySortName, webtor.LibrarySortYear}
		}
		return []webtor.LibrarySort{webtor.LibrarySortRecent, webtor.LibrarySortName}
	}
	ti, si := 0, 0
	for {
		sorts := sortsFor(types[ti])
		si = si % len(sorts)
		resp, err := c.LibraryList(ctx, webtor.LibraryListOptions{Type: types[ti], Sort: sorts[si]})
		if err != nil {
			return err
		}
		if len(resp.Items) == 0 && types[ti] == webtor.LibraryTypeAll {
			_, _ = fmt.Fprintln(os.Stderr, "the library is empty — `webtor add` something first")
			return nil
		}
		items := []picker.Item{
			{Label: fmt.Sprintf("[ show: %s ]", types[ti]), Detail: "enter switches: all → movies → series"},
			{Label: fmt.Sprintf("[ sort: %s ]", sorts[si]), Detail: "enter switches: " + sortHint(sorts)},
		}
		for _, it := range resp.Items {
			items = append(items, picker.Item{Label: it.Name,
				Detail: render.Size(it.Size) + ", added " + it.AddedAt.Format("2006-01-02")})
		}
		if len(resp.Items) == 0 {
			items = append(items, picker.Item{Label: fmt.Sprintf("(no %s in the library)", types[ti])})
		}
		def := 0
		if len(resp.Items) > 0 {
			def = 2
		}
		n, err := picker.Pick(fmt.Sprintf("Library (%s, %s):", types[ti], sorts[si]), items, def)
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case n == 0:
			ti = (ti + 1) % len(types)
		case n == 1:
			si = (si + 1) % len(sorts)
		case n-2 < len(resp.Items):
			if err := resourceMenu(ctx, cmd, c, resp.Items[n-2].ResourceID); err != nil && !back(err) {
				return err
			}
		}
	}
}

func sortHint(sorts []webtor.LibrarySort) string {
	parts := make([]string, len(sorts))
	for i, x := range sorts {
		parts[i] = string(x)
	}
	return strings.Join(parts, " → ")
}

// libraryPrint is the scripted no-subcommand fallback: same as `library ls`
// with defaults.
func libraryPrint(ctx context.Context, cmd *cli.Command, c *webtor.Client) error {
	resp, err := c.LibraryList(ctx, webtor.LibraryListOptions{})
	if err != nil {
		return err
	}
	if cmd.Bool("json") {
		return render.JSON(os.Stdout, resp)
	}
	rows := make([][]string, 0, len(resp.Items))
	for _, it := range resp.Items {
		rows = append(rows, []string{it.ResourceID, render.Size(it.Size),
			it.AddedAt.Format("2006-01-02"), it.Name})
	}
	render.Table(os.Stdout, []string{"RESOURCE", "SIZE", "ADDED", "NAME"}, rows)
	return nil
}

func libraryCmd() *cli.Command {
	return &cli.Command{
		Name:    "library",
		Aliases: []string{"lib"},
		Usage: "manage the account library (webtor.io accounts only)",
		Description: "Without a subcommand: an interactive browser on a terminal (pick an\n" +
			"entry, then play / download / inspect / rename / remove it), a plain\n" +
			"listing otherwise.",
		Action: libraryInteractive,
		Commands: []*cli.Command{
			{
				Name:  "ls",
				Usage: "list the library",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "type", Value: "all", Usage: "all, movies or series"},
					&cli.StringFlag{Name: "sort", Value: "recent", Usage: "recent, name, or year (movies/series only)"},
					&cli.IntFlag{Name: "limit", Usage: "page size (server default 100)"},
					&cli.IntFlag{Name: "offset"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, _, err := newClient(ctx, cmd)
					if err != nil {
						return err
					}
					resp, err := c.LibraryList(ctx, webtor.LibraryListOptions{
						Type:   webtor.LibraryType(cmd.String("type")),
						Sort:   webtor.LibrarySort(cmd.String("sort")),
						Limit:  cmd.Int("limit"),
						Offset: cmd.Int("offset"),
					})
					if err != nil {
						return err
					}
					if cmd.Bool("json") {
						return render.JSON(os.Stdout, resp)
					}
					rows := make([][]string, 0, len(resp.Items))
					for _, it := range resp.Items {
						rows = append(rows, []string{
							it.ResourceID, render.Size(it.Size),
							it.AddedAt.Format("2006-01-02"), it.Name,
						})
					}
					render.Table(os.Stdout, []string{"RESOURCE", "SIZE", "ADDED", "NAME"}, rows)
					if len(resp.Items) < resp.Count {
						_, _ = fmt.Fprintf(os.Stderr, "…%d of %d items — use --offset\n", len(resp.Items), resp.Count)
					}
					return nil
				},
			},
			{
				Name:      "add",
				Usage:     "add a stored resource to the library",
				ArgsUsage: "<resource-id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					rid, err := resourceIDArg(cmd)
					if err != nil {
						return err
					}
					c, _, err := newClient(ctx, cmd)
					if err != nil {
						return err
					}
					item, err := c.LibraryAdd(ctx, rid)
					if err != nil {
						return err
					}
					if cmd.Bool("json") {
						return render.JSON(os.Stdout, item)
					}
					_, _ = fmt.Fprintf(os.Stderr, "in library: %s\n", item.Name)
					return nil
				},
			},
			{
				Name:      "rm",
				Usage:     "remove a resource from the library",
				ArgsUsage: "<resource-id>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					rid, err := resourceIDArg(cmd)
					if err != nil {
						return err
					}
					c, _, err := newClient(ctx, cmd)
					if err != nil {
						return err
					}
					if err := c.LibraryRemove(ctx, rid); err != nil {
						return err
					}
					_, _ = fmt.Fprintln(os.Stderr, "removed")
					return nil
				},
			},
			{
				Name:      "rename",
				Usage:     "rename a library entry (also in WebDAV and S3 views)",
				ArgsUsage: "<resource-id> <name>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					raw, rest, err := resourceAndRest(cmd, true)
					if err != nil {
						return err
					}
					rid := extractResourceID(raw)
					if len(rest) != 1 {
						return exitcode.Usagef("usage: library rename <resource-id> <name> (the id may come from stdin)")
					}
					name := rest[0]
					c, _, err := newClient(ctx, cmd)
					if err != nil {
						return err
					}
					item, err := c.LibraryRename(ctx, rid, name)
					if err != nil {
						return err
					}
					if cmd.Bool("json") {
						return render.JSON(os.Stdout, item)
					}
					_, _ = fmt.Fprintf(os.Stderr, "renamed to %s\n", item.Name)
					return nil
				},
			},
		},
	}
}
