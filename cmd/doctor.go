package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/config"
	"github.com/webtor-io/webtor-cli/internal/notify"
	"github.com/webtor-io/webtor-cli/internal/render"
)

// The reference torrent every check leans on: Sintel is public, permanently
// cached and small enough to sample.
const doctorRid = "08ada5a7a6183aae1e09d831df6748d566095a10"

type checkState string

const (
	checkOK   checkState = "ok"
	checkWarn checkState = "warn"
	checkFail checkState = "fail"
)

type check struct {
	Name   string     `json:"name"`
	State  checkState `json:"state"`
	Detail string     `json:"detail"`
	// Hint is what to do about it, on the failures where that is knowable.
	Hint string `json:"hint,omitempty"`
}

func doctorCmd() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "check the setup: configuration, API, speed, player, output folder",
		Description: "Runs the checks a support answer would ask for and prints them as a\n" +
			"list. Exit code 1 if anything failed, 0 otherwise (warnings do not\n" +
			"fail). --json prints the same checks as data.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "no-speed", Usage: "skip the download speed sample"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			checks := runDoctor(ctx, cmd)
			if cmd.Bool("json") {
				if err := render.JSON(os.Stdout, map[string]any{"checks": checks}); err != nil {
					return err
				}
			} else {
				printChecks(checks)
			}
			for _, c := range checks {
				if c.State == checkFail {
					// Already reported line by line; exit non-zero without
					// repeating it as an error message.
					return &silentFailure{}
				}
			}
			return nil
		},
	}
}

// silentFailure marks "the checks failed" without printing anything twice.
type silentFailure struct{}

func (e *silentFailure) Error() string { return "" }

func printChecks(checks []check) {
	rows := make([][]string, 0, len(checks))
	for _, c := range checks {
		mark := "✓"
		switch c.State {
		case checkWarn:
			mark = "!"
		case checkFail:
			mark = "✗"
		}
		detail := c.Detail
		if c.Hint != "" {
			detail += " — " + c.Hint
		}
		rows = append(rows, []string{mark, c.Name, detail})
	}
	render.Table(os.Stdout, nil, rows)
}

func runDoctor(ctx context.Context, cmd *cli.Command) []check {
	var checks []check
	add := func(c check) { checks = append(checks, c) }

	// 1. Configuration.
	r, err := config.Resolve(cmd.String("context"))
	if err != nil {
		add(check{Name: "configuration", State: checkFail, Detail: err.Error(),
			Hint: "run `webtor config init`"})
		return checks
	}
	where := "config file"
	if r.FromEnv {
		where = "WEBTOR_* environment"
	}
	add(check{Name: "configuration", State: checkOK,
		Detail: fmt.Sprintf("context %q, backend %s, from the %s", r.Name, r.Context.Backend, where)})

	// 2. Where the key lives (file contexts only — env mode holds no secret).
	if !r.FromEnv {
		_, creds, cerr := config.Load()
		switch {
		case cerr != nil:
			add(check{Name: "credentials", State: checkWarn, Detail: cerr.Error()})
		case config.CredentialSource(r.Name, creds) == "keyring":
			add(check{Name: "credentials", State: checkOK, Detail: "stored in the OS keyring"})
		case config.CredentialSource(r.Name, creds) == "file":
			add(check{Name: "credentials", State: checkWarn,
				Detail: "stored in credentials.yaml (0600)",
				Hint:   "no keyring available here; `webtor auth login` moves it once there is one"})
		default:
			add(check{Name: "credentials", State: checkWarn, Detail: "no key stored",
				Hint: "run `webtor auth login`"})
		}
	}

	// 3. The API itself.
	c, _, err := newClient(ctx, cmd)
	if err != nil {
		add(check{Name: "api", State: checkFail, Detail: err.Error()})
		return checks
	}
	start := time.Now()
	res, err := c.Resource(ctx, doctorRid)
	rtt := time.Since(start).Round(time.Millisecond)
	switch {
	case err == nil:
		add(check{Name: "api", State: checkOK,
			Detail: fmt.Sprintf("reachable, %s round-trip", rtt)})
	case webtor.IsUnauthorized(err):
		add(check{Name: "api", State: checkFail, Detail: "the key was rejected",
			Hint: "run `webtor auth login`"})
		return checks
	case webtor.IsPaymentRequired(err):
		add(check{Name: "api", State: checkFail, Detail: "the account has no paid plan",
			Hint: "upgrade at https://webtor.io/donate"})
		return checks
	default:
		add(check{Name: "api", State: checkFail, Detail: err.Error()})
		return checks
	}

	// 4. Account, where the backend has one.
	if c.Supports(webtor.CapProfile) {
		if p, perr := c.Profile(ctx); perr == nil {
			add(check{Name: "account", State: checkOK,
				Detail: fmt.Sprintf("%s, %s plan", p.Email, p.Tier.Name)})
		} else {
			add(check{Name: "account", State: checkWarn, Detail: perr.Error()})
		}
	} else {
		add(check{Name: "account", State: checkWarn,
			Detail: string(r.Context.Backend) + " backend has no account surface",
			Hint:   "library, vault and profile need a webtor.io context"})
	}

	// 5. Transfer speed from the edge that would serve a real download.
	if !cmd.Bool("no-speed") {
		add(speedCheck(ctx, c, res))
	}

	// 6. Player.
	player := cmd.String("player")
	if player == "" {
		player = "vlc"
	}
	switch {
	case lookPlayer(player) != "":
		add(check{Name: "player", State: checkOK, Detail: player + " found"})
	default:
		add(check{Name: "player", State: checkWarn, Detail: player + " not found",
			Hint: "install it or pass --player mpv"})
	}

	// 7. Download folder.
	add(folderCheck(cmd))

	// 8. Notifications.
	if notify.Enabled() {
		add(check{Name: "notifications", State: checkOK, Detail: "desktop notifications available"})
	} else {
		add(check{Name: "notifications", State: checkWarn, Detail: "no notification helper",
			Hint: "install notify-send (Linux); WEBTOR_NO_NOTIFY disables this on purpose"})
	}

	return checks
}

// speedCheck samples the first megabyte of a known file through the same
// export URL a real download would use.
func speedCheck(ctx context.Context, c *webtor.Client, res *webtor.ResourceResponse) check {
	item := res.File
	if item == nil {
		limit := min(max(res.FilesCount, 1), 1000)
		for it, err := range c.ListAll(ctx, doctorRid, webtor.ListOptions{
			Output: webtor.ListOutputFlat, Limit: limit}) {
			if err != nil {
				return check{Name: "speed", State: checkWarn, Detail: err.Error()}
			}
			if it.MediaFormat == webtor.MediaFormatVideo && (item == nil || it.Size > item.Size) {
				v := it
				item = &v
			}
		}
	}
	if item == nil {
		return check{Name: "speed", State: checkWarn, Detail: "no sample file in the reference torrent"}
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	d, err := c.OpenDownload(ctx, doctorRid, item.ID)
	if err != nil {
		return check{Name: "speed", State: checkFail, Detail: err.Error()}
	}
	defer func() { _ = d.Close() }()

	const sample = 1 << 20
	start := time.Now()
	n, err := io.CopyN(io.Discard, d, sample)
	elapsed := time.Since(start)
	if err != nil && n == 0 {
		return check{Name: "speed", State: checkFail, Detail: err.Error()}
	}
	mbps := float64(n) * 8 / elapsed.Seconds() / 1e6
	detail := fmt.Sprintf("%.1f Mbps on the first %s", mbps, render.Size(n))
	switch {
	case mbps < 2:
		return check{Name: "speed", State: checkWarn, Detail: detail,
			Hint: "slow link or a cold torrent; a paid plan lifts the rate limit"}
	default:
		return check{Name: "speed", State: checkOK, Detail: detail}
	}
}

func folderCheck(cmd *cli.Command) check {
	dir := outputBase(cmd)
	shown := dir
	if dir == "" {
		dir, shown = ".", "the current directory"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return check{Name: "download folder", State: checkFail,
			Detail: shown + ": " + err.Error()}
	}
	probe := filepath.Join(dir, ".webtor-write-test")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return check{Name: "download folder", State: checkFail,
			Detail: shown + " is not writable", Hint: err.Error()}
	}
	_ = os.Remove(probe)
	free := freeSpace(dir)
	detail := shown + " is writable"
	if free > 0 {
		detail += ", " + render.Size(free) + " free"
		if free < 1<<30 {
			return check{Name: "download folder", State: checkWarn, Detail: detail,
				Hint: "less than 1 GB left"}
		}
	}
	return check{Name: "download folder", State: checkOK, Detail: detail}
}

// lookPlayer mirrors launchPlayer's discovery without starting anything.
func lookPlayer(player string) string {
	if p, err := exec.LookPath(player); err == nil {
		return p
	}
	if app := macApp(player); app != "" {
		return app
	}
	return ""
}
