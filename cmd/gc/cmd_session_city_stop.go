package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/session"
	"github.com/spf13/cobra"
)

type sessionClearCityStopOptions struct {
	ExpectedHeldUntil string
	CheckOnly         bool
	JSON              bool
}

func newSessionClearCityStopCmd(stdout, stderr io.Writer) *cobra.Command {
	var opts sessionClearCityStopOptions
	cmd := &cobra.Command{
		Use:   "clear-city-stop <session-id>",
		Short: "Value-fence removal of a stale city-stop latch",
		Long: `Clear sleep_reason=city-stop on one exact future-held session.
The command requires a direct bead ID and exact held_until value, observes runtime
liveness, and uses metadata value-CAS. It never falls back to an unconditional
write. --check validates the same predicates without writing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdSessionClearCityStop(args, opts, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.ExpectedHeldUntil, "if-held-until", "", "required exact future held_until value")
	cmd.Flags().BoolVar(&opts.CheckOnly, "check", false, "validate every precondition without writing")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "emit JSONL")
	_ = cmd.MarkFlagRequired("if-held-until")
	return cmd
}

func cmdSessionClearCityStop(args []string, opts sessionClearCityStopOptions, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(stderr, "gc session clear-city-stop: exact session bead ID required") //nolint:errcheck // best-effort stderr
		return 1
	}
	id := strings.TrimSpace(args[0])
	store, code := openCityStore(stderr, "gc session clear-city-stop")
	if store == nil {
		return code
	}
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc session clear-city-stop: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	cfg, err := loadCityConfig(cityPath, configWarnWriter(opts.JSON, stderr))
	if err != nil {
		fmt.Fprintf(stderr, "gc session clear-city-stop: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	sessStore := cliSessionStore(store, cfg, cityPath)
	bead, err := sessStore.Get(id)
	if err != nil || bead.ID != id || !session.IsSessionBeadOrRepairable(bead) {
		if err == nil {
			err = fmt.Errorf("%q is not a session bead", id)
		}
		fmt.Fprintf(stderr, "gc session clear-city-stop: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	sp, err := newSessionProvider()
	if err != nil {
		fmt.Fprintf(stderr, "gc session clear-city-stop: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	handle, err := workerHandleForSessionWithConfig(cityPath, sessStore, sp, cfg, id)
	if err != nil {
		fmt.Fprintf(stderr, "gc session clear-city-stop: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	obs, err := handle.LiveObservation(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "gc session clear-city-stop: runtime observation: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	req := session.ClearCityStopRequest{ExpectedHeldUntil: strings.TrimSpace(opts.ExpectedHeldUntil), RuntimeRunning: obs.Running, RuntimeAlive: obs.Alive, Now: time.Now().UTC()}
	front := sessionFrontDoor(sessStore)
	action := "clear-city-stop"
	if opts.CheckOnly {
		action = "clear-city-stop-check"
		err = front.CheckClearCityStop(id, req)
	} else {
		err = front.ClearCityStop(id, req)
	}
	if err != nil {
		fmt.Fprintf(stderr, "gc session clear-city-stop: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	if opts.JSON {
		if err := writeSessionActionJSON(stdout, sessionActionResult{Command: "session clear-city-stop", Action: action, SessionID: id, State: string(session.State(bead.Metadata["state"]))}); err != nil {
			fmt.Fprintf(stderr, "gc session clear-city-stop: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "Session %s city-stop latch %s.\n", id, map[bool]string{true: "validated", false: "cleared"}[opts.CheckOnly]) //nolint:errcheck // best-effort stdout
	return 0
}
