package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/render"
)

func vaultCmd() *cli.Command {
	return &cli.Command{
		Name:  "vault",
		Usage: "long-term storage pledges (webtor.io accounts only)",
		Commands: []*cli.Command{
			{
				Name:  "status",
				Usage: "show points, content counters and pledges",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					c, _, err := newClient(ctx, cmd)
					if err != nil {
						return err
					}
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
					rid, err := resourceIDArg(cmd, 0)
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
					rid, err := resourceIDArg(cmd, 0)
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
