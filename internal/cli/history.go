package cli

import (
	"encoding/json"
	"fmt"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
)

func newHistoryCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "history <task>", Short: "Show a task's saved timeline and transcript", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := app.Open(cmd.Context(), ".", config.DefaultPaths())
		if err != nil {
			return err
		}
		defer s.Store.Close()
		h, err := s.History(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(h)
		}
		w := cmd.OutOrStdout()
		fmt.Fprintln(w, ansi.Strip(h.Task.Title))
		for _, e := range h.Events {
			fmt.Fprintf(w, "%s  %s\n", e.TS.Local().Format("2006-01-02 15:04:05"), e.Kind)
		}
		for _, session := range h.Sessions {
			fmt.Fprintf(w, "\n%s · session %d · %s\n", session.Agent, session.ID, session.StartedAt.Format("2006-01-02 15:04:05"))
			for _, t := range h.Turns {
				if t.SessionID == session.ID {
					fmt.Fprintf(w, "\n[%s]\n%s\n", t.Role, ansi.Strip(t.Content))
				}
			}
		}
		for _, x := range h.Handoffs {
			state := "delivered"
			if x.DeliveredAt == nil {
				state = "pending"
			}
			fmt.Fprintf(w, "\nHandoff to %s (%s)\n", x.Agent, state)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output task, sessions, turns, events and handoffs as JSON")
	return cmd
}

func newSwitchCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{Use: "switch <task> -a <agent>", Short: "Open the TUI and switch agents with a context handoff", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return app.RunSwitch(cmd.Context(), ".", args[0], target, cmd.OutOrStdout())
	}}
	cmd.Flags().StringVarP(&target, "agent", "a", "", "Configured target agent")
	_ = cmd.MarkFlagRequired("agent")
	return cmd
}
