package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
)

var indexRe = regexp.MustCompile(`^\d+$`)

func downloadCmd() *cli.Command {
	return &cli.Command{
		Name:      "download",
		Usage:     "download files (with resume) or a directory archive",
		ArgsUsage: "<resource-id> [CONTENT-ID | PATH ...]",
		Description: "Without content arguments, downloads the whole torrent: directly for a\n" +
			"single-file torrent, as an archive otherwise. Partial local files resume\n" +
			"from where they stopped.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "output directory (or file name for a single target)"},
			&cli.StringFlag{Name: "archive", Usage: "force an archive download: zip or tar"},
			&cli.BoolFlag{Name: "stdout", Usage: "write the payload to stdout"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			raw, args, err := resourceAndRest(cmd)
			if err != nil {
				return err
			}
			rid := extractResourceID(raw)
			c, _, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}
			archive := cmd.String("archive")
			if archive != "" && archive != "zip" && archive != "tar" {
				return exitcode.Usagef("--archive must be zip or tar")
			}

			res, err := c.Resource(ctx, rid)
			if err != nil {
				return err
			}

			// Whole-torrent invocation.
			if len(args) == 0 {
				if res.File != nil && archive == "" {
					return downloadOne(ctx, cmd, c, rid, res.File.ID)
				}
				if archive == "" {
					archive = "tar"
				}
				return downloadArchive(ctx, cmd, c, rid, res.ID, archive, nil)
			}

			// Path arguments that name directories become one archive; files
			// download individually.
			var fileIDs []string
			var dirPaths []string
			for _, a := range args {
				item, err := resolveContent(ctx, c, rid, a)
				if err != nil {
					return err
				}
				if item.Type == webtor.ListTypeDirectory {
					dirPaths = append(dirPaths, item.Path)
					continue
				}
				fileIDs = append(fileIDs, item.ID)
			}
			if len(dirPaths) > 0 && len(fileIDs) > 0 {
				return exitcode.Usagef("mixing files and directories in one download is not supported — run two invocations")
			}
			if len(dirPaths) > 0 {
				if archive == "" {
					archive = "tar"
				}
				return downloadArchive(ctx, cmd, c, rid, res.ID, archive, dirPaths)
			}
			if cmd.Bool("stdout") && len(fileIDs) > 1 {
				return exitcode.Usagef("--stdout takes exactly one file")
			}
			for _, id := range fileIDs {
				if err := downloadOne(ctx, cmd, c, rid, id); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// resolveContent turns a CLI content argument — a listing id, a numeric file
// index, or a path inside the torrent — into the matching item.
func resolveContent(ctx context.Context, c *webtor.Client, rid, arg string) (*webtor.ListItem, error) {
	if !strings.Contains(arg, "/") {
		// Try it as a direct content id (listing id or numeric index): the
		// export endpoint accepts both. Confirm existence via export of the
		// download type only.
		resp, err := c.Export(ctx, rid, arg, webtor.ExportOptions{Types: []webtor.ExportType{webtor.ExportTypeDownload}})
		if err == nil {
			item := resp.Source
			if indexRe.MatchString(arg) && item.ID != "" {
				arg = item.ID
			}
			return &item, nil
		}
		if !webtor.IsNotFound(err) {
			return nil, err
		}
	}
	// Fall back to a path lookup over the flat listing.
	want := "/" + strings.Trim(arg, "/")
	for it, err := range c.ListAll(ctx, rid, webtor.ListOptions{Output: webtor.ListOutputFlat}) {
		if err != nil {
			return nil, err
		}
		if it.Path == want || strings.TrimPrefix(it.Path, "/") == strings.TrimPrefix(want, "/") {
			return &it, nil
		}
		// A directory prefix match makes the argument a directory.
		if strings.HasPrefix(it.Path, want+"/") {
			return &webtor.ListItem{ID: want, Path: want, Type: webtor.ListTypeDirectory}, nil
		}
	}
	return nil, exitcode.Usagef("no file or directory %q in this torrent (try `webtor ls %s`)", arg, rid)
}

func downloadOne(ctx context.Context, cmd *cli.Command, c *webtor.Client, rid, contentID string) error {
	var offset int64
	var dest string

	toStdout := cmd.Bool("stdout")
	if !toStdout {
		// Peek at the descriptor to know name/size before opening the stream,
		// so an existing partial file can resume.
		probe, err := c.Export(ctx, rid, contentID, webtor.ExportOptions{Types: []webtor.ExportType{webtor.ExportTypeDownload}})
		if err != nil {
			return err
		}
		dest = destPath(cmd, probe.Source.Name)
		if st, err := os.Stat(dest); err == nil {
			switch {
			case st.Size() == probe.Source.Size:
				_, _ = fmt.Fprintf(os.Stderr, "%s: already complete\n", dest)
				return nil
			case st.Size() < probe.Source.Size:
				offset = st.Size()
			}
		}
	}

	d, err := c.OpenDownload(ctx, rid, contentID, webtor.WithOffset(offset))
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	var out io.Writer
	var closeOut func() error = func() error { return nil }
	if toStdout {
		out = os.Stdout
	} else {
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		if offset > 0 {
			_, _ = fmt.Fprintf(os.Stderr, "%s: resuming at %s\n", dest, render.Size(offset))
		}
		out, closeOut = f, f.Close
	}

	label := d.Name
	if label == "" {
		label = contentID
	}
	bar, finish := render.NewProgress(os.Stderr, label, d.Size-offset,
		render.IsTTY(os.Stderr), cmd.Bool("quiet") || cmd.Bool("json"))
	_, err = io.Copy(io.MultiWriter(out, bar), d)
	finish()
	if cerr := closeOut(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if cmd.Bool("json") && !toStdout {
		return render.JSON(os.Stdout, map[string]any{"file": dest, "bytes": d.BytesRead() + offset})
	}
	return nil
}

func downloadArchive(ctx context.Context, cmd *cli.Command, c *webtor.Client, rid, contentID, format string, paths []string) error {
	rc, name, err := c.OpenArchive(ctx, rid, contentID, webtor.ArchiveFormat(format), paths)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	var out io.Writer
	var closeOut func() error = func() error { return nil }
	dest := ""
	if cmd.Bool("stdout") {
		out = os.Stdout
	} else {
		dest = destPath(cmd, name)
		f, err := os.Create(dest)
		if err != nil {
			return err
		}
		out, closeOut = f, f.Close
	}

	// Archives are packed on the fly: no size, no resume — spinner-less copy
	// with milestone output suppressed (size 0 renders nothing).
	bar, finish := render.NewProgress(os.Stderr, name, -1,
		render.IsTTY(os.Stderr), cmd.Bool("quiet") || cmd.Bool("json"))
	n, err := io.Copy(io.MultiWriter(out, bar), rc)
	finish()
	if cerr := closeOut(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if cmd.Bool("json") && dest != "" {
		return render.JSON(os.Stdout, map[string]any{"file": dest, "bytes": n})
	}
	return nil
}

// destPath places name under -o (or the working directory). When -o names a
// file (has an extension or exists as a file), it is used verbatim.
func destPath(cmd *cli.Command, name string) string {
	o := cmd.String("output")
	if o == "" {
		return name
	}
	if st, err := os.Stat(o); err == nil && st.IsDir() {
		return filepath.Join(o, name)
	}
	if strings.HasSuffix(o, string(os.PathSeparator)) {
		_ = os.MkdirAll(o, 0o755)
		return filepath.Join(o, name)
	}
	return o
}
