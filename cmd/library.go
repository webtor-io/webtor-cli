package cmd

import (
	"bufio"
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
	in := bufio.NewReader(os.Stdin)
	for {
		resp, err := c.LibraryList(ctx, webtor.LibraryListOptions{})
		if err != nil {
			return err
		}
		if len(resp.Items) == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "the library is empty — `webtor add` something first")
			return nil
		}
		items := make([]picker.Item, 0, len(resp.Items)+1)
		for _, it := range resp.Items {
			items = append(items, picker.Item{Label: it.Name,
				Detail: render.Size(it.Size) + ", added " + it.AddedAt.Format("2006-01-02")})
		}
		items = append(items, picker.Item{Label: "— quit —"})
		n, err := picker.Pick(os.Stdin, os.Stderr, "Library:", items, -1)
		if err != nil {
			return err
		}
		if n == len(resp.Items) {
			return nil
		}
		entry := resp.Items[n]
		if err := libraryEntryMenu(ctx, cmd, c, in, entry); err != nil {
			return err
		}
	}
}

func libraryEntryMenu(ctx context.Context, cmd *cli.Command, c *webtor.Client, in *bufio.Reader, entry webtor.LibraryItem) error {
	actions := []picker.Item{
		{Label: "play"},
		{Label: "download all files"},
		{Label: "show files"},
		{Label: "rename"},
		{Label: "remove from library"},
		{Label: "back"},
	}
	n, err := picker.Pick(os.Stdin, os.Stderr, entry.Name+":", actions, 0)
	if err != nil {
		return err
	}
	switch actions[n].Label {
	case "play":
		res, err := c.Resource(ctx, entry.ResourceID)
		if err != nil {
			return err
		}
		return runPlay(ctx, cmd, c, res, "")
	case "download all files":
		files, err := listFiles(ctx, c, entry.ResourceID, entry.FilesCount)
		if err != nil {
			return err
		}
		return downloadFiles(ctx, cmd, c, entry.ResourceID, files, true)
	case "show files":
		files, err := listFiles(ctx, c, entry.ResourceID, entry.FilesCount)
		if err != nil {
			return err
		}
		printItems(files)
	case "rename":
		_, _ = fmt.Fprintf(os.Stderr, "New name [%s]: ", entry.Name)
		line, err := in.ReadString('\n')
		if err != nil {
			return err
		}
		if name := strings.TrimSpace(line); name != "" {
			if _, err := c.LibraryRename(ctx, entry.ResourceID, name); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(os.Stderr, "renamed")
		}
	case "remove from library":
		_, _ = fmt.Fprintf(os.Stderr, "Remove %q from the library? [y/N]: ", entry.Name)
		line, _ := in.ReadString('\n')
		if strings.EqualFold(strings.TrimSpace(line), "y") {
			if err := c.LibraryRemove(ctx, entry.ResourceID); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(os.Stderr, "removed")
		}
	}
	return nil
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
					&cli.StringFlag{Name: "sort", Value: "recent", Usage: "recent or name"},
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
