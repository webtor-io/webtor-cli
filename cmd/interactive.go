package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/config"
	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
)

// The interactive screens form a stack: Esc (picker.ErrBack) pops one
// screen, Ctrl-C (picker.ErrCancelled) unwinds everything and exits. Every
// loop below follows the same rule: catch ErrBack → return nil (up one
// level), let ErrCancelled propagate.

// back reports (and consumes) the one-screen-back signal.
func back(err error) bool { return errors.Is(err, picker.ErrBack) }

// interactive reports whether prompting the person is appropriate: a real
// terminal on both ends and no machine-output flags.
// WEBTOR_FORCE_INTERACTIVE=1 overrides the TTY check so the test suite can
// drive the menus through the numbered fallback with piped answers.
func interactive(cmd *cli.Command) bool {
	if cmd.Bool("quiet") || cmd.Bool("json") {
		return false
	}
	if os.Getenv("WEBTOR_FORCE_INTERACTIVE") != "" {
		return true
	}
	return render.IsTTY(os.Stdin) && render.IsTTY(os.Stderr)
}

// topMenu is what bare `webtor` opens on a terminal.
func topMenu(ctx context.Context, cmd *cli.Command, c *webtor.Client) error {
	last := 0
	for {
		items := []picker.Item{
			{Label: "library", Detail: "your saved torrents"},
			{Label: "vault", Detail: "long-term storage pledges"},
			{Label: "scan a folder", Detail: "local .torrent files"},
			{Label: "profile", Detail: "account and plan"},
			{Label: "quit"},
		}
		n, err := picker.Pick("webtor:", items, last)
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
		last = n
		switch items[n].Label {
		case "library":
			err = libraryBrowse(ctx, cmd, c)
		case "vault":
			err = vaultBrowse(ctx, cmd, c)
		case "scan a folder":
			err = scanFromMenu(ctx, cmd)
		case "profile":
			err = showProfile(ctx, c)
		case "quit":
			return nil
		}
		if err != nil && !back(err) {
			return err
		}
	}
}

// scanFromMenu asks for the folder (Enter = the configured download folder)
// and opens the scan browser.
func scanFromMenu(ctx context.Context, cmd *cli.Command) error {
	def := "."
	if currentCfg != nil && currentCfg.DownloadDir != "" {
		def = currentCfg.DownloadDir
	}
	dir, err := picker.ReadLine(fmt.Sprintf("Folder to scan [%s]: ", def))
	if err != nil {
		return err
	}
	if dir == "" {
		dir = def
	}
	infos, skipped, err := scanDir(config.ExpandHome(dir))
	if err != nil {
		return err
	}
	if skipped > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "skipped %d unparsable .torrent file(s)\n", skipped)
	}
	if len(infos) == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "no .torrent files under %s\n", dir)
		return nil
	}
	return scanBrowse(ctx, cmd, dir, infos)
}

func showProfile(ctx context.Context, c *webtor.Client) error {
	p, err := c.Profile(ctx)
	if err != nil {
		return err
	}
	return picker.Show("Profile:", []string{
		"email:      " + p.Email,
		"tier:       " + p.Tier.Name,
		"scopes:     " + strings.Join(p.Scopes, ","),
		"show adult: " + fmt.Sprintf("%v", p.Settings.ShowAdult),
		"user id:    " + p.UserID,
	})
}

// pause waits for Enter so printed output survives the next screen redraw.
func pause() error {
	_, err := picker.ReadLine("press Enter to continue…")
	return err
}

// resourceMenu is the shared per-torrent action screen — the same one
// whether the torrent was reached from the library, from the vault, or via
// `webtor <resource-id>`. The action list is contextual: library and vault
// entries appear only on backends that have those surfaces, and flip
// between add/remove according to the current state.
func resourceMenu(ctx context.Context, cmd *cli.Command, c *webtor.Client, rid string) error {
	res, err := c.Resource(ctx, rid)
	if err != nil {
		return err
	}
	last := 0
	for {
		name := res.Name
		var inLibrary bool
		if c.Supports(webtor.CapLibrary) {
			if entry, err := c.LibraryGet(ctx, rid); err == nil {
				inLibrary = true
				name = entry.Name
			} else if !webtor.IsNotFound(err) {
				return err
			}
		}
		var pledge *webtor.PledgeStatusResponse
		if c.Supports(webtor.CapVault) {
			st, err := c.VaultPledgeStatus(ctx, rid)
			switch {
			case err == nil:
				pledge = st
			case !webtor.IsNotFound(err):
				return err
			}
		}

		var items []picker.Item
		add := func(label, detail string) { items = append(items, picker.Item{Label: label, Detail: detail}) }
		add("play", "")
		add("browse files", fmt.Sprintf("%d files, %s", res.FilesCount, render.Size(res.Size)))
		add("download all files", destLabel(cmd))
		if c.Supports(webtor.CapLibrary) {
			if inLibrary {
				add("remove from library", "")
				add("rename", "")
			} else {
				add("add to library", "")
			}
		}
		switch {
		case pledge == nil && c.Supports(webtor.CapVault):
			add("vault: pledge", "keep it stored long-term")
		case pledge != nil && pledge.Status == webtor.PledgeStatusVaulted:
			add("vault: withdraw pledge", "stored")
		case pledge != nil:
			add("vault: watch transfer", pledge.Status)
			add("vault: withdraw pledge", "")
		}
		add("back", "")

		n, err := picker.Pick(name+":", items, min(last, len(items)-1))
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
		last = n
		switch items[n].Label {
		case "play":
			err = runPlay(ctx, cmd, c, res, "")
		case "browse files":
			err = browseFiles(ctx, cmd, c, res)
		case "download all files":
			var files []webtor.ListItem
			if files, err = listFiles(ctx, c, rid, res.FilesCount); err == nil {
				err = downloadFiles(ctx, cmd, c, rid, files, true)
			}
		case "add to library":
			if _, err = c.LibraryAdd(ctx, rid); err == nil {
				_, _ = fmt.Fprintln(os.Stderr, "added to the library")
			}
		case "remove from library":
			if confirm(fmt.Sprintf("Remove %q from the library?", name)) {
				if err = c.LibraryRemove(ctx, rid); err == nil {
					_, _ = fmt.Fprintln(os.Stderr, "removed")
				}
			}
		case "rename":
			line, rerr := picker.ReadLine(fmt.Sprintf("New name [%s]: ", name))
			if rerr != nil {
				return rerr
			}
			if newName := line; newName != "" {
				if _, err = c.LibraryRename(ctx, rid, newName); err == nil {
					_, _ = fmt.Fprintln(os.Stderr, "renamed")
				}
			}
		case "vault: pledge":
			if _, err = c.VaultPledge(ctx, rid); err == nil {
				_, _ = fmt.Fprintln(os.Stderr, "pledged — the transfer runs in the background")
			}
		case "vault: watch transfer":
			err = waitVaulted(ctx, cmd, c, rid)
		case "vault: withdraw pledge":
			if confirm(fmt.Sprintf("Withdraw the pledge for %q?", name)) {
				if err = c.VaultUnpledge(ctx, rid); err == nil {
					_, _ = fmt.Fprintln(os.Stderr, "pledge withdrawn")
				}
			}
		case "back":
			return nil
		}
		if err != nil && !back(err) {
			return err
		}
	}
}

func confirm(question string) bool {
	line, _ := picker.ReadLine(question + " [y/N]: ")
	return strings.EqualFold(line, "y")
}

// browseRow is one entry of a browser level.
type browseRow struct {
	label, detail string
	isDir         bool
	file          *webtor.ListItem
}

// browseLevel returns the entries visible at dir (a prefix without leading
// or trailing slash, "" = root): subdirectories first, then files.
func browseLevel(files []webtor.ListItem, dir string) []browseRow {
	var dirs []string
	seen := map[string]bool{}
	var fileRows []browseRow
	for i, f := range files {
		path := strings.TrimPrefix(f.Path, "/")
		rel := path
		if dir != "" {
			if !strings.HasPrefix(path, dir+"/") {
				continue
			}
			rel = strings.TrimPrefix(path, dir+"/")
		}
		if name, rest, found := strings.Cut(rel, "/"); found && rest != "" {
			if !seen[name] {
				seen[name] = true
				dirs = append(dirs, name)
			}
		} else {
			fileRows = append(fileRows, browseRow{label: rel, detail: render.Size(f.Size), file: &files[i]})
		}
	}
	sort.Strings(dirs)
	rows := make([]browseRow, 0, len(dirs)+len(fileRows))
	for _, d := range dirs {
		rows = append(rows, browseRow{label: d + "/", isDir: true})
	}
	return append(rows, fileRows...)
}

// browseFiles walks the torrent's directory tree: Enter descends into a
// directory or opens the per-file menu, Esc goes one directory up and out of
// the browser at the top. Wrapper-only levels (the single top-level folder
// most torrents ship) are skipped on the way in, and Esc treats the first
// real level as the top.
func browseFiles(ctx context.Context, cmd *cli.Command, c *webtor.Client, res *webtor.ResourceResponse) error {
	files, err := listFiles(ctx, c, res.ID, res.FilesCount)
	if err != nil {
		return err
	}
	base := ""
	for {
		rows := browseLevel(files, base)
		if len(rows) != 1 || !rows[0].isDir {
			break
		}
		base = joinDir(base, strings.TrimSuffix(rows[0].label, "/"))
	}
	dir := base
	lastAt := map[string]int{} // remembered cursor per directory
	for {
		rows := browseLevel(files, dir)
		items := make([]picker.Item, len(rows))
		for i, r := range rows {
			items[i] = picker.Item{Label: r.label, Detail: r.detail}
		}
		title := res.Name + "/"
		if dir != "" {
			title = dir + "/"
		}
		n, err := picker.Pick(title, items, min(lastAt[dir], max(len(items)-1, 0)))
		if back(err) {
			if dir == base {
				return nil
			}
			parent := ""
			if i := strings.LastIndex(dir, "/"); i > 0 {
				parent = dir[:i]
			}
			if len(parent) < len(base) {
				return nil
			}
			dir = parent
			continue
		}
		if err != nil {
			return err
		}
		lastAt[dir] = n
		picked := rows[n]
		if picked.isDir {
			dir = joinDir(dir, strings.TrimSuffix(picked.label, "/"))
			continue
		}
		if err := fileMenu(ctx, cmd, c, res.ID, picked.file); err != nil && !back(err) {
			return err
		}
	}
}

func joinDir(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// fileMenu is the per-file action screen inside the browser.
func fileMenu(ctx context.Context, cmd *cli.Command, c *webtor.Client, rid string, item *webtor.ListItem) error {
	last := 0
	for {
		items := []picker.Item{
			{Label: "play", Detail: ""},
			{Label: "download", Detail: destLabel(cmd)},
			{Label: "show download url", Detail: "short-lived"},
			{Label: "back"},
		}
		n, err := picker.Pick(strings.TrimPrefix(item.Path, "/")+":", items, last)
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
		last = n
		switch items[n].Label {
		case "play":
			err = playItem(ctx, cmd, c, rid, item, nil)
		case "download":
			err = downloadFiles(ctx, cmd, c, rid, []webtor.ListItem{*item}, false)
		case "show download url":
			u, uerr := downloadURLFor(ctx, c, rid, item, nil)
			if uerr != nil {
				return uerr
			}
			_, _ = fmt.Println(u)
			err = pause()
		case "back":
			return nil
		}
		if err != nil && !back(err) {
			return err
		}
	}
}
