package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func init() {
	c := &cobra.Command{
		Use:    "man DIR",
		Short:  "Write man pages into DIR (e.g. ~/.local/share/man/man1)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := os.MkdirAll(args[0], 0o755); err != nil {
				return err
			}
			t := time.Now()
			h := &doc.GenManHeader{Title: "BUDGETCTL", Section: "1", Date: &t, Source: "missionctl"}
			if err := doc.GenManTree(rootCmd, h, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "man pages written to %s\n", args[0])
			return nil
		},
	}
	rootCmd.AddCommand(c)
}
