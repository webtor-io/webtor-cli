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
	for {
		items := []picker.Item{
			{Label: "library", Detail: "your saved torrents"},
			{Label: "vault", Detail: "long-term storage pledges"},
			{Label: "profile", Detail: "account and plan"},
			{Label: "quit"},
		}
		n, err := picker.Pick("webtor:", items, 0)
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
		switch items[n].Label {
		case "library":
			err = libraryBrowse(ctx, cmd, c)
		case "vault":
			err = vaultBrowse(ctx, cmd, c)
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

func showProfile(ctx context.Context, c *webtor.Client) error {
	p, err := c.Profile(ctx)
	if err != nil {
		return err
	}
	printProfile(p)
	return pause()
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
		add("download all files", "into the current directory")
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

		n, err := picker.Pick(name+":", items, 0)
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
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

// browseFiles walks the torrent's directory tree: Enter descends into a
// directory or opens the per-file menu, Esc goes one directory up (and out
// of the browser at the root).
func browseFiles(ctx context.Context, cmd *cli.Command, c *webtor.Client, res *webtor.ResourceResponse) error {
	files, err := listFiles(ctx, c, res.ID, res.FilesCount)
	if err != nil {
		return err
	}
	dir := "" // current prefix without trailing slash, "" = root
	for {
		type row struct {
			label, detail string
			isDir         bool
			file          *webtor.ListItem
		}
		var dirs []string
		seen := map[string]bool{}
		var rows []row
		for i, f := range files {
			rel := strings.TrimPrefix(strings.TrimPrefix(f.Path, "/"), strings.TrimPrefix(dir+"/", "/"))
			if dir != "" && !strings.HasPrefix(strings.TrimPrefix(f.Path, "/"), strings.TrimPrefix(dir, "/")+"/") {
				continue
			}
			if dir == "" {
				rel = strings.TrimPrefix(f.Path, "/")
			}
			if name, rest, found := strings.Cut(rel, "/"); found && rest != "" {
				if !seen[name] {
					seen[name] = true
					dirs = append(dirs, name)
				}
			} else {
				rows = append(rows, row{label: rel, detail: render.Size(f.Size), file: &files[i]})
			}
		}
		sort.Strings(dirs)
		items := make([]picker.Item, 0, len(dirs)+len(rows))
		all := make([]row, 0, len(dirs)+len(rows))
		for _, d := range dirs {
			all = append(all, row{label: d + "/", isDir: true})
			items = append(items, picker.Item{Label: d + "/"})
		}
		for _, r := range rows {
			all = append(all, r)
			items = append(items, picker.Item{Label: r.label, Detail: r.detail})
		}
		title := res.Name + "/"
		if dir != "" {
			title = strings.TrimPrefix(dir, "/") + "/"
		}
		n, err := picker.Pick(title, items, 0)
		if back(err) {
			if dir == "" {
				return nil
			}
			if i := strings.LastIndex(dir, "/"); i > 0 {
				dir = dir[:i]
			} else {
				dir = ""
			}
			continue
		}
		if err != nil {
			return err
		}
		picked := all[n]
		if picked.isDir {
			if dir == "" {
				dir = strings.TrimSuffix(picked.label, "/")
			} else {
				dir = dir + "/" + strings.TrimSuffix(picked.label, "/")
			}
			continue
		}
		if err := fileMenu(ctx, cmd, c, res.ID, picked.file); err != nil && !back(err) {
			return err
		}
	}
}

// fileMenu is the per-file action screen inside the browser.
func fileMenu(ctx context.Context, cmd *cli.Command, c *webtor.Client, rid string, item *webtor.ListItem) error {
	for {
		items := []picker.Item{
			{Label: "play", Detail: ""},
			{Label: "download", Detail: "into the current directory"},
			{Label: "show download url", Detail: "short-lived"},
			{Label: "back"},
		}
		n, err := picker.Pick(strings.TrimPrefix(item.Path, "/")+":", items, 0)
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
		switch items[n].Label {
		case "play":
			u, uerr := downloadURLFor(ctx, c, rid, item, nil)
			if uerr != nil {
				return uerr
			}
			err = launchWithPlayer(cmd, u, item.Path)
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
