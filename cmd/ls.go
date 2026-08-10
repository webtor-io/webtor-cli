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

func lsCmd() *cli.Command {
	return &cli.Command{
		Name:      "ls",
		Usage:     "list a torrent's content",
		ArgsUsage: "<resource-id> [PATH]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "tree", Usage: "list one directory level (with PATH to descend)"},
			&cli.BoolFlag{Name: "all", Usage: "fetch every page"},
			&cli.IntFlag{Name: "limit", Value: 100, Usage: "page size"},
			&cli.IntFlag{Name: "offset", Usage: "page offset"},
			&cli.StringFlag{Name: "sort", Usage: "sort order: name or size (default: torrent order)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			raw, rest, err := resourceAndRest(cmd, true)
			if err != nil {
				return err
			}
			rid := extractResourceID(raw)
			c, _, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}
			path := ""
			if len(rest) > 0 {
				path = rest[0]
			}
			o := webtor.ListOptions{
				Path:   path,
				Limit:  cmd.Int("limit"),
				Offset: cmd.Int("offset"),
				Output: webtor.ListOutputFlat,
				Sort:   webtor.ListSort(cmd.String("sort")),
			}
			if cmd.Bool("tree") {
				o.Output = webtor.ListOutputTree
			}
			if o.Sort != "" && o.Sort != webtor.ListSortName && o.Sort != webtor.ListSortSize {
				return exitcode.Usagef("--sort must be name or size")
			}

			if cmd.Bool("all") {
				var items []webtor.ListItem
				for it, err := range c.ListAll(ctx, rid, o) {
					if err != nil {
						return err
					}
					items = append(items, it)
				}
				if cmd.Bool("json") {
					return render.JSON(os.Stdout, items)
				}
				printItems(items)
				return nil
			}

			resp, err := c.List(ctx, rid, o)
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return render.JSON(os.Stdout, resp)
			}
			printItems(resp.Items)
			if len(resp.Items) < resp.Count {
				_, _ = fmt.Fprintf(os.Stderr, "…%d of %d items — use --all or --offset\n", len(resp.Items), resp.Count)
			}
			return nil
		},
	}
}

func printItems(items []webtor.ListItem) {
	rows := make([][]string, 0, len(items))
	for _, it := range items {
		name := it.Path
		size := render.Size(it.Size)
		if it.Type == webtor.ListTypeDirectory {
			name += "/"
		}
		rows = append(rows, []string{it.ID, size, name})
	}
	render.Table(os.Stdout, []string{"ID", "SIZE", "PATH"}, rows)
}
