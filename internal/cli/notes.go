package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/store"
)

func newNotesCmd() *cobra.Command {
	var value, file string
	cmd := &cobra.Command{Use: "notes <task>", Short: "Show or replace task notes", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := app.Open(cmd.Context(), ".", config.DefaultPaths())
		if err != nil {
			return err
		}
		defer s.Store.Close()
		if !cmd.Flags().Changed("set") && !cmd.Flags().Changed("file") {
			task, err := s.Find(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), ansi.Strip(task.Notes))
			return err
		}
		if cmd.Flags().Changed("file") {
			reader := cmd.InOrStdin()
			if file != "-" {
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer f.Close()
				reader = f
			}
			b, err := io.ReadAll(io.LimitReader(reader, store.MaxNotesBytes+1))
			if err != nil {
				return err
			}
			value = string(b)
		}
		if err = s.SetNotes(cmd.Context(), args[0], value); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Notes saved.")
		return err
	}}
	cmd.Flags().StringVar(&value, "set", "", "Replace notes (an empty string clears them)")
	cmd.Flags().StringVar(&file, "file", "", "Read replacement notes from a file, or - for stdin")
	cmd.MarkFlagsMutuallyExclusive("set", "file")
	return cmd
}
