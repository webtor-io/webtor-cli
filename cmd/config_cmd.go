package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
	"github.com/webtor-io/webtor-cli/internal/config"
	"github.com/webtor-io/webtor-cli/internal/exitcode"
	"github.com/webtor-io/webtor-cli/internal/render"
	"github.com/webtor-io/webtor-cli/internal/wizard"
)

func configCmd() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "manage the CLI configuration",
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "run the interactive setup",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return wizard.Run(ctx, wizard.IO{In: os.Stdin, Out: os.Stderr})
				},
			},
			{
				Name:  "show",
				Usage: "print the configuration (keys masked)",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, creds, err := config.Load()
					if err != nil {
						return &exitcode.UsageError{Msg: err.Error()}
					}
					type shown struct {
						config.Context `yaml:",inline" json:",inline"`
						Key            string `yaml:"key,omitempty" json:"key,omitempty"`
					}
					out := map[string]any{"current": cfg.Current, "dir": config.Dir()}
					cxs := map[string]shown{}
					for name, cx := range cfg.Contexts {
						s := shown{Context: cx}
						if c := creds[name]; c != nil && c.APIKey != "" {
							s.Key = mask(c.APIKey)
						}
						cxs[name] = s
					}
					out["contexts"] = cxs
					if cmd.Bool("json") {
						return render.JSON(os.Stdout, out)
					}
					_, _ = fmt.Printf("dir:     %s\ncurrent: %s\n", config.Dir(), cfg.Current)
					var rows [][]string
					for name, s := range cxs {
						rows = append(rows, []string{name, string(s.Backend), s.BaseURL, s.Key})
					}
					render.Table(os.Stdout, []string{"CONTEXT", "BACKEND", "BASE URL", "KEY"}, rows)
					return nil
				},
			},
			{
				Name:      "use",
				Usage:     "switch the current context",
				ArgsUsage: "<context>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					name := cmd.Args().First()
					if name == "" {
						return exitcode.Usagef("missing <context> argument")
					}
					cfg, creds, err := config.Load()
					if err != nil {
						return &exitcode.UsageError{Msg: err.Error()}
					}
					if _, ok := cfg.Contexts[name]; !ok {
						return exitcode.Usagef("context %q not found", name)
					}
					cfg.Current = name
					if err := config.Save(cfg, creds); err != nil {
						return err
					}
					_, _ = fmt.Fprintf(os.Stderr, "switched to context %q\n", name)
					return nil
				},
			},
		},
	}
}
