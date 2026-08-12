package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
)

func downloadCmd() *cli.Command {
	return &cli.Command{
		Name:      "download",
		Aliases:   []string{"dl"},
		Usage:     "download files, mirroring the torrent's directory layout",
		ArgsUsage: "<resource-id> [CONTENT-ID | PATH ...]",
		Description: "Without content arguments the whole torrent is downloaded, file by\n" +
			"file, into the output directory (default: the current one) with the\n" +
			"torrent's directory structure preserved. Directory arguments download\n" +
			"their files the same way; a single explicit file lands under its own\n" +
			"name. Partial local files resume from where they stopped.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "output directory (or file name for a single explicit file)"},
			&cli.BoolFlag{Name: "stdout", Usage: "write the payload to stdout (single file only)"},
			&cli.BoolFlag{Name: "interactive", Aliases: []string{"i"}, Usage: "pick the files from a list (answers may be piped)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			raw, args, err := resourceAndRest(cmd, true)
			if err != nil {
				return err
			}
			rid := extractResourceID(raw)
			c, _, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}
			res, err := c.Resource(ctx, rid)
			if err != nil {
				return err
			}

			// Explicit single file: keep the wget-like shape — the file lands
			// under its own name (or exactly at -o).
			if len(args) == 1 {
				item, _, err := resolveContent(ctx, c, rid, args[0])
				if err != nil {
					return err
				}
				if item.Type != webtor.ListTypeDirectory {
					return downloadFiles(ctx, cmd, c, rid, []webtor.ListItem{*item}, false)
				}
			}
			if cmd.Bool("stdout") {
				return exitcode.Usagef("--stdout takes exactly one file")
			}

			// Everything else downloads file by file with the torrent's
			// directory layout preserved under the output directory.
			var prefixes []string
			for _, a := range args {
				item, _, err := resolveContent(ctx, c, rid, a)
				if err != nil {
					return err
				}
				if item.Type == webtor.ListTypeDirectory {
					prefixes = append(prefixes, strings.TrimSuffix(item.Path, "/")+"/")
				} else {
					prefixes = append(prefixes, item.Path)
				}
			}

			all, err := listFiles(ctx, c, rid, res.FilesCount)
			if err != nil {
				return err
			}
			var files []webtor.ListItem
			for _, it := range all {
				if len(prefixes) == 0 || matchesAny(it.Path, prefixes) {
					files = append(files, it)
				}
			}
			if len(files) == 0 {
				return exitcode.Usagef("nothing to download (try `webtor ls %s`)", rid)
			}
			if cmd.Bool("interactive") && len(files) > 1 {
				items := make([]picker.Item, len(files))
				for i, it := range files {
					items[i] = picker.Item{Label: strings.TrimPrefix(it.Path, "/"), Detail: render.Size(it.Size)}
				}
				picked, err := picker.PickMulti(os.Stdin, os.Stderr, "Which files?", items)
				if err != nil {
					return err
				}
				sel := make([]webtor.ListItem, 0, len(picked))
				for _, n := range picked {
					sel = append(sel, files[n])
				}
				files = sel
			}
			return downloadFiles(ctx, cmd, c, rid, files, true)
		},
	}
}

// listFiles returns every file of the resource, one page when the count
// allows it (the server caps a page at 1000).
func listFiles(ctx context.Context, c *webtor.Client, rid string, filesCount int) ([]webtor.ListItem, error) {
	limit := min(max(filesCount, 1), 1000)
	var files []webtor.ListItem
	for it, err := range c.ListAll(ctx, rid, webtor.ListOptions{Output: webtor.ListOutputFlat, Limit: limit}) {
		if err != nil {
			return nil, err
		}
		if it.Type == webtor.ListTypeFile {
			files = append(files, it)
		}
	}
	return files, nil
}

func matchesAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// resolveContent turns a CLI content argument — a listing id, a numeric file
// index, or a path inside the torrent — into the matching item. When the
// resolution itself required a download-type export (the direct-id branch),
// that ExportResponse is returned too, so callers that need a URL can reuse
// it instead of paying for a second export.
func resolveContent(ctx context.Context, c *webtor.Client, rid, arg string) (*webtor.ListItem, *webtor.ExportResponse, error) {
	if !strings.Contains(arg, "/") {
		// Try it as a direct content id (listing id or numeric index): the
		// export endpoint accepts both. Confirm existence via export of the
		// download type only.
		resp, err := c.Export(ctx, rid, arg, webtor.ExportOptions{Types: []webtor.ExportType{webtor.ExportTypeDownload}})
		if err == nil {
			item := resp.Source
			return &item, resp, nil
		}
		if !webtor.IsNotFound(err) {
			return nil, nil, err
		}
	}
	// Fall back to a path lookup over the flat listing.
	want := "/" + strings.Trim(arg, "/")
	for it, err := range c.ListAll(ctx, rid, webtor.ListOptions{Output: webtor.ListOutputFlat}) {
		if err != nil {
			return nil, nil, err
		}
		if it.Path == want || strings.TrimPrefix(it.Path, "/") == strings.TrimPrefix(want, "/") {
			return &it, nil, nil
		}
		// A directory prefix match makes the argument a directory.
		if strings.HasPrefix(it.Path, want+"/") {
			return &webtor.ListItem{ID: want, Path: want, Type: webtor.ListTypeDirectory}, nil, nil
		}
	}
	return nil, nil, exitcode.Usagef("no file or directory %q in this torrent (try `webtor ls %s`)", arg, rid)
}

// downloadURLFor resolves item's download URL, reusing resp when the
// resolution step already fetched one.
func downloadURLFor(ctx context.Context, c *webtor.Client, rid string, item *webtor.ListItem, resp *webtor.ExportResponse) (string, error) {
	if resp == nil {
		var err error
		resp, err = c.Export(ctx, rid, item.ID, webtor.ExportOptions{Types: []webtor.ExportType{webtor.ExportTypeDownload}})
		if err != nil {
			return "", err
		}
	}
	u, ok := resp.DownloadURL()
	if !ok {
		return "", &webtor.Error{HTTPStatus: 404, Code: webtor.CodeNotFound,
			Message: fmt.Sprintf("no download export for %q", item.Path)}
	}
	return u, nil
}

// downloadFiles fetches the given files one by one. With layout, each file
// lands under the output directory at its torrent path; otherwise (the
// single explicit file) under its own name via destPath, or on stdout.
func downloadFiles(ctx context.Context, cmd *cli.Command, c *webtor.Client, rid string, files []webtor.ListItem, layout bool) error {
	type report struct {
		File  string `json:"file"`
		Bytes int64  `json:"bytes"`
	}
	var reports []report
	var total int64
	for _, it := range files {
		var dest string
		switch {
		case cmd.Bool("stdout"):
			dest = ""
		case layout:
			dest = filepath.Join(cmd.String("output"), filepath.FromSlash(strings.TrimPrefix(it.Path, "/")))
		default:
			name := it.Name
			if name == "" {
				name = filepath.Base(it.Path)
			}
			dest = destPath(cmd, name)
		}
		n, err := downloadOne(ctx, cmd, c, rid, &it, dest)
		if err != nil {
			return fmt.Errorf("%s: %w", strings.TrimPrefix(it.Path, "/"), err)
		}
		total += n
		if dest != "" {
			reports = append(reports, report{File: dest, Bytes: n})
		}
	}
	if cmd.Bool("json") && len(reports) > 0 {
		return render.JSON(os.Stdout, map[string]any{"files": reports, "bytes": total})
	}
	if layout && !cmd.Bool("quiet") && !cmd.Bool("json") {
		_, _ = fmt.Fprintf(os.Stderr, "downloaded %d file(s), %s\n", len(reports), render.Size(total))
	}
	return nil
}

// downloadOne fetches one file to dest ("" = stdout), resuming a partial
// local file, and returns the file's on-disk size when done. The item
// already carries name and size, so no probe request is needed.
func downloadOne(ctx context.Context, cmd *cli.Command, c *webtor.Client, rid string, item *webtor.ListItem, dest string) (int64, error) {
	var offset int64
	toStdout := dest == ""
	if !toStdout {
		if st, err := os.Stat(dest); err == nil {
			switch {
			case st.Size() == item.Size:
				if !cmd.Bool("quiet") && !cmd.Bool("json") {
					_, _ = fmt.Fprintf(os.Stderr, "%s: already complete\n", dest)
				}
				return item.Size, nil
			case st.Size() < item.Size:
				offset = st.Size()
			}
		}
		if dir := filepath.Dir(dest); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return 0, err
			}
		}
	}

	d, err := c.OpenDownload(ctx, rid, item.ID, webtor.WithOffset(offset))
	if err != nil {
		return 0, err
	}
	defer func() { _ = d.Close() }()

	var out io.Writer
	var closeOut func() error = func() error { return nil }
	if toStdout {
		out = os.Stdout
	} else {
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return 0, err
		}
		if offset > 0 && !cmd.Bool("quiet") && !cmd.Bool("json") {
			_, _ = fmt.Fprintf(os.Stderr, "%s: resuming at %s\n", dest, render.Size(offset))
		}
		out, closeOut = f, f.Close
	}

	label := d.Name
	if label == "" {
		label = item.ID
	}
	bar, finish := render.NewProgress(os.Stderr, label, d.Size-offset,
		render.IsTTY(os.Stderr), cmd.Bool("quiet") || cmd.Bool("json"))
	_, err = io.Copy(io.MultiWriter(out, bar), d)
	finish()
	if cerr := closeOut(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	return d.BytesRead() + offset, nil
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
