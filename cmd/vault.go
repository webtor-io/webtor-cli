package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/picker"
	"github.com/webtor-io/webtor-cli/internal/render"
)

func vaultCmd() *cli.Command {
	return &cli.Command{
		Name:    "vault",
		Aliases: []string{"v"},
		Usage: "long-term storage pledges (webtor.io accounts only)",
		Description: "Without a subcommand: an interactive pledge browser on a terminal\n" +
			"(watch a transfer, withdraw a pledge), the plain status otherwise.",
		Action: vaultInteractive,
		Commands: []*cli.Command{
			{
				Name:  "status",
				Usage: "show points, content counters and pledges",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, _, err := newClient(ctx, cmd)
					if err != nil {
						return err
					}
					return vaultStatus(ctx, cmd, c)
				},
			},
			{
				Name:      "pledge",
				Usage:     "pledge points to keep a resource stored (1 VP per GB)",
				ArgsUsage: "<resource-id>",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "wait", Usage: "poll until the transfer completes"},
					&cli.DurationFlag{Name: "poll-interval", Value: 15 * time.Second, Hidden: true},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					rid, err := resourceIDArg(cmd)
					if err != nil {
						return err
					}
					c, _, err := newClient(ctx, cmd)
					if err != nil {
						return err
					}
					p, err := c.VaultPledge(ctx, rid)
					switch {
					case err == nil:
						if !cmd.Bool("json") {
							_, _ = fmt.Fprintf(os.Stderr, "pledged %.0f VP for %s\n", p.Amount, p.Name)
						}
					case webtor.IsConflict(err) && cmd.Bool("wait"):
						// Already pledged — waiting is still what was asked for.
					default:
						return err
					}
					if !cmd.Bool("wait") {
						if cmd.Bool("json") && p != nil {
							return render.JSON(os.Stdout, p)
						}
						return nil
					}
					return waitVaulted(ctx, cmd, c, rid)
				},
			},
			{
				Name:      "unpledge",
				Usage:     "withdraw a pledge and claim the points back",
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
					if err := c.VaultUnpledge(ctx, rid); err != nil {
						return err
					}
					_, _ = fmt.Fprintln(os.Stderr, "pledge withdrawn")
					return nil
				},
			},
		},
	}
}

// vaultStatus prints the account's Vault state (the `vault status` body,
// shared with the scripted no-subcommand fallback).
func vaultStatus(ctx context.Context, cmd *cli.Command, c *webtor.Client) error {
	v, err := c.Vault(ctx)
	if err != nil {
		return err
	}
	if cmd.Bool("json") {
		return render.JSON(os.Stdout, v)
	}
	points := "unlimited"
	if v.Points.Total != nil {
		avail := 0.0
		if v.Points.Available != nil {
			avail = *v.Points.Available
		}
		points = fmt.Sprintf("%.0f of %.0f available", avail, *v.Points.Total)
	}
	_, _ = fmt.Printf("points:  %s (%.0f funded, %.0f frozen, %.0f claimable)\n",
		points, v.Points.Funded, v.Points.Frozen, v.Points.Claimable)
	_, _ = fmt.Printf("content: %d vaulted, %d loading, %d expiring\n",
		v.Content.Vaulted, v.Content.Loading, v.Content.Expiring)
	if len(v.Pledges) > 0 {
		rows := make([][]string, 0, len(v.Pledges))
		for _, p := range v.Pledges {
			rows = append(rows, []string{p.ResourceID, fmt.Sprintf("%.0f VP", p.Amount),
				pledgeState(p), p.Name})
		}
		render.Table(os.Stdout, []string{"RESOURCE", "PLEDGE", "STATE", "NAME"}, rows)
	}
	return nil
}

// vaultInteractive is the no-subcommand entry: an interactive pledge browser
// on a terminal, the plain status when scripted.
func vaultInteractive(ctx context.Context, cmd *cli.Command) error {
	c, _, err := newClient(ctx, cmd)
	if err != nil {
		return err
	}
	if !interactive(cmd) {
		return vaultStatus(ctx, cmd, c)
	}
	err = vaultBrowse(ctx, cmd, c)
	if back(err) {
		return nil
	}
	return err
}

// vaultBrowse lists the pledges; a picked entry opens the same shared
// resourceMenu the library uses. Esc goes back to the caller.
func vaultBrowse(ctx context.Context, cmd *cli.Command, c *webtor.Client) error {
	for {
		v, err := c.Vault(ctx)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(os.Stderr, "points: %.0f funded, %.0f claimable · content: %d vaulted, %d loading\n",
			v.Points.Funded, v.Points.Claimable, v.Content.Vaulted, v.Content.Loading)
		if len(v.Pledges) == 0 {
			_, _ = fmt.Fprintln(os.Stderr, "no pledges — `webtor vault pledge <resource-id>` starts one")
			return nil
		}
		items := make([]picker.Item, 0, len(v.Pledges))
		for _, p := range v.Pledges {
			items = append(items, picker.Item{Label: p.Name,
				Detail: fmt.Sprintf("%.1f VP, %s", p.Amount, pledgeState(p))})
		}
		n, err := picker.Pick("Vault pledges:", items, -1)
		if back(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := resourceMenu(ctx, cmd, c, v.Pledges[n].ResourceID); err != nil && !back(err) {
			return err
		}
	}
}

func pledgeState(p webtor.Pledge) string {
	switch {
	case p.Vaulted:
		return "vaulted"
	case p.Expired:
		return "expired"
	case p.Frozen:
		return "frozen"
	case p.Funded:
		return "loading"
	default:
		return "waiting"
	}
}

// waitVaulted polls the pledge status until a terminal state. A failed
// status is reported but polling continues: storage retries on its own
// schedule, so failed is terminal for the attempt, not for the resource.
func waitVaulted(ctx context.Context, cmd *cli.Command, c *webtor.Client, rid string) error {
	interval := cmd.Duration("poll-interval")
	if interval <= 0 {
		// Callers outside `vault pledge` (the interactive menus) have no
		// poll-interval flag; zero would spin the poll loop hot.
		interval = 15 * time.Second
	}
	for {
		st, err := c.VaultPledgeStatus(ctx, rid)
		if err != nil {
			return err
		}
		switch st.Status {
		case webtor.PledgeStatusVaulted:
			if cmd.Bool("json") {
				return render.JSON(os.Stdout, st)
			}
			_, _ = fmt.Fprintln(os.Stderr, "vaulted")
			return nil
		case webtor.PledgeStatusExpired:
			return &webtor.Error{HTTPStatus: 409, Code: webtor.CodeConflict,
				Message: "the pledge expired (resource lost funding)"}
		case webtor.PledgeStatusFailed:
			if !cmd.Bool("quiet") && !cmd.Bool("json") {
				_, _ = fmt.Fprintln(os.Stderr, "transfer attempt failed — storage retries on its own, still waiting")
			}
		default:
			if !cmd.Bool("quiet") && !cmd.Bool("json") {
				if st.Progress != nil {
					_, _ = fmt.Fprintf(os.Stderr, "%s %.1f%% (%s / %s)\n", st.Status, *st.Progress,
						render.Size(zeroIfNil(st.StoredSize)), render.Size(zeroIfNil(st.TotalSize)))
				} else {
					_, _ = fmt.Fprintln(os.Stderr, st.Status)
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func zeroIfNil(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
