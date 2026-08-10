package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
)

func libraryCmd() *cli.Command {
	return &cli.Command{
		Name:  "library",
		Usage: "manage the account library (webtor.io accounts only)",
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
