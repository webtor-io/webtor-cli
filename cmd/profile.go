package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	webtor "github.com/webtor-io/api-sdk-go"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
)

func profileCmd() *cli.Command {
	return &cli.Command{
		Name:  "profile",
		Usage: "show or update the account profile (webtor.io accounts only)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "set-show-adult", Usage: "update the show-adult setting: true or false"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			c, _, err := newClient(ctx, cmd)
			if err != nil {
				return err
			}
			var p *webtor.ProfileResponse
			if v := cmd.String("set-show-adult"); v != "" {
				b := v == "true"
				if !b && v != "false" {
					return exitcode.Usagef("--set-show-adult takes true or false")
				}
				p, err = c.UpdateProfile(ctx, webtor.ProfileUpdate{ShowAdult: &b})
			} else {
				p, err = c.Profile(ctx)
			}
			if err != nil {
				return err
			}
			if cmd.Bool("json") {
				return render.JSON(os.Stdout, p)
			}
			render.Table(os.Stdout, nil, [][]string{
				{"user", p.UserID},
				{"email", p.Email},
				{"tier", p.Tier.Name},
				{"scopes", strings.Join(p.Scopes, ",")},
				{"show_adult", fmt.Sprintf("%v", p.Settings.ShowAdult)},
			})
			return nil
		},
	}
}
