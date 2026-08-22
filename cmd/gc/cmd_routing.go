package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	gcapi "github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/routingdecision"
	"github.com/spf13/cobra"
)

const (
	routingIngestFileLimit  = 1 << 20
	routingGrantOutputLimit = 8 << 10
	routingGrantTimeout     = 30 * time.Second
)

type routingGrantRunner func(context.Context, string, gcapi.GrantBinding) (string, error)

var (
	routingAPIClientHook                      = resolveRoutingAPIClient
	routingGrantCommandRun routingGrantRunner = runRoutingGrantCommand
)

func newRoutingCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "routing",
		Short: "Inspect and ingest live durable routing decisions",
	}
	cmd.AddCommand(
		newRoutingStatusCmd(stdout, stderr),
		newRoutingTargetsCmd(stdout, stderr),
		newRoutingEligibleCmd(stdout, stderr),
		newRoutingDecisionsCmd(stdout, stderr),
		newRoutingOutcomesCmd(stdout, stderr),
		newRoutingIngestCmd(stdout, stderr),
	)
	return cmd
}

func resolveRoutingAPIClient() (*gcapi.Client, error) {
	resolved, err := resolveContextAllowRemote()
	if err != nil {
		return nil, err
	}
	if resolved.Remote != nil {
		return buildRemoteClient(resolved.Remote)
	}
	client, reason := maintenanceAPIClient(resolved.CityPath)
	if client == nil {
		return nil, fmt.Errorf("live routing API unavailable (%s)", reason)
	}
	return client, nil
}

func routingClient(stderr io.Writer, operation string) (*gcapi.Client, error) {
	client, err := routingAPIClientHook()
	if err != nil {
		fmt.Fprintf(stderr, "gc routing %s: %v\n", operation, err) //nolint:errcheck
		return nil, errExit
	}
	return client, nil
}

func newRoutingStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "status", Short: "Show live routing authority and ledger status", Args: cobra.NoArgs}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		client, err := routingClient(stderr, "status")
		if err != nil {
			return err
		}
		status, err := client.RoutingStatus()
		if err != nil {
			fmt.Fprintf(stderr, "gc routing status: %v\n", err) //nolint:errcheck
			return errExit
		}
		if jsonOutput {
			return writeCLIJSONLine(stdout, status)
		}
		fmt.Fprintf(stdout, "Routing schema: %d\nStatus: %s\nReason: %s\nAuthority ready: %t\nRetention: %d calendar months (%s)\nLedger schema: %d\nStore revision: %d\n", //nolint:errcheck
			status.Schema, status.Status, status.Reason, status.AuthorityReady, status.RetentionMonths, status.TerminalStateBasis,
			status.Store.SchemaVersion, status.Store.StoreRevision)
		fmt.Fprintln(stdout, "State counts:") //nolint:errcheck
		for _, count := range status.Store.StateCounts {
			fmt.Fprintf(stdout, "  %-20s %d\n", count.State, count.Count) //nolint:errcheck
		}
		return nil
	}
	return cmd
}

func newRoutingTargetsCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "targets", Short: "List deterministic selection-safe targets", Args: cobra.NoArgs}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		client, err := routingClient(stderr, "targets")
		if err != nil {
			return err
		}
		items, err := client.RoutingTargets()
		if err != nil {
			fmt.Fprintf(stderr, "gc routing targets: %v\n", err) //nolint:errcheck
			return errExit
		}
		if jsonOutput {
			return writeCLIJSONLine(stdout, struct {
				Items []routingdecision.TargetSnapshot `json:"items"`
			}{Items: items})
		}
		return writeRoutingTargetsTable(stdout, items)
	}
	return cmd
}

func writeRoutingTargetsTable(stdout io.Writer, items []routingdecision.TargetSnapshot) error {
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RIG\tTARGET\tPROVIDER\tCONFIG DIGEST\tDESCRIPTION") //nolint:errcheck
	for _, item := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", item.Rig, item.Target, item.ResolvedProvider, item.ConfigDigest, item.Description) //nolint:errcheck
	}
	return tw.Flush()
}

func newRoutingEligibleCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "eligible", Short: "Show deterministic eligible work and target inputs", Args: cobra.NoArgs}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		client, err := routingClient(stderr, "eligible")
		if err != nil {
			return err
		}
		selection, err := client.RoutingEligible()
		if err != nil {
			fmt.Fprintf(stderr, "gc routing eligible: %v\n", err) //nolint:errcheck
			return errExit
		}
		if jsonOutput {
			return writeCLIJSONLine(stdout, selection)
		}
		fmt.Fprintf(stdout, "Observed: %s\n", selection.ObservedAt.UTC().Format(time.RFC3339Nano)) //nolint:errcheck
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "RIG\tWORK\tREVISION\tCLAIM FENCE\tSTATE DIGEST") //nolint:errcheck
		for _, item := range selection.Work {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", item.Rig, item.WorkBeadID, item.WorkRevision, item.ClaimFence, item.WorkStateDigest) //nolint:errcheck
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Targets:") //nolint:errcheck
		return writeRoutingTargetsTable(stdout, selection.Targets)
	}
	return cmd
}

func newRoutingDecisionsCmd(stdout, stderr io.Writer) *cobra.Command {
	var state, cursor string
	var limit int
	var jsonOutput bool
	cmd := &cobra.Command{Use: "decisions", Short: "List durable routing decisions", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&state, "state", "", "Filter by exact lifecycle state")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum rows to scan and return (1-256)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Opaque decision-ID cursor")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		if limit < 1 || limit > 256 || state != "" && !routingStateKnown(routingdecision.State(state)) {
			fmt.Fprintln(stderr, "gc routing decisions: invalid --state or --limit (want 1-256)") //nolint:errcheck
			return errExit
		}
		client, err := routingClient(stderr, "decisions")
		if err != nil {
			return err
		}
		page, err := client.RoutingDecisions(gcapi.RoutingDecisionListRequest{State: routingdecision.State(state), Limit: limit, Cursor: cursor})
		if err != nil {
			fmt.Fprintf(stderr, "gc routing decisions: %v\n", err) //nolint:errcheck
			return errExit
		}
		if jsonOutput {
			return writeCLIJSONLine(stdout, page)
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "DECISION\tSTATE\tRECORD REVISION\tWORK\tTARGET") //nolint:errcheck
		for _, item := range page.Items {
			record := item.Record
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", record.Payload.DecisionID, record.State, record.RecordRevision, record.Payload.WorkBeadID, record.Payload.Target) //nolint:errcheck
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Total: %d\n", page.Total) //nolint:errcheck
		if page.NextCursor != "" {
			fmt.Fprintf(stdout, "Next cursor: %s\n", page.NextCursor) //nolint:errcheck
		}
		return nil
	}
	return cmd
}

func routingStateKnown(state routingdecision.State) bool {
	for _, known := range routingdecision.AllStates() {
		if state == known {
			return true
		}
	}
	return false
}

func newRoutingOutcomesCmd(stdout, stderr io.Writer) *cobra.Command {
	var cursor string
	var limit int
	var jsonOutput bool
	cmd := &cobra.Command{Use: "outcomes", Short: "List authoritative redacted recommendation outcomes", Args: cobra.NoArgs}
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum rows to return (1-100)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Opaque decision-ID cursor")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		if limit < 1 || limit > 100 {
			fmt.Fprintln(stderr, "gc routing outcomes: invalid --limit (want 1-100)") //nolint:errcheck
			return errExit
		}
		client, err := routingClient(stderr, "outcomes")
		if err != nil {
			return err
		}
		page, err := client.RoutingOutcomes(gcapi.RoutingOutcomeListRequest{Limit: limit, Cursor: cursor})
		if err != nil {
			fmt.Fprintf(stderr, "gc routing outcomes: %v\n", err) //nolint:errcheck
			return errExit
		}
		if jsonOutput {
			return writeCLIJSONLine(stdout, page)
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "RECOMMENDATION	DECISION	WORK	STATUS	DISPOSITION	COVERAGE") //nolint:errcheck
		for _, item := range page.Items {
			decisionID := "-"
			if item.RoutingDecisionID != nil {
				decisionID = *item.RoutingDecisionID
			}
			fmt.Fprintf(tw, "%s	%s	%s	%s	%s	%s\n", item.RecommendationID, decisionID, item.WorkID, item.Status, item.Disposition, item.Coverage) //nolint:errcheck
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if page.NextCursor != "" {
			fmt.Fprintf(stdout, "Next cursor: %s\n", page.NextCursor) //nolint:errcheck
		}
		fmt.Fprintf(stdout, "Partial: %t\n", page.Partial) //nolint:errcheck
		return nil
	}
	return cmd
}

func newRoutingIngestCmd(stdout, stderr io.Writer) *cobra.Command {
	var file, idempotencyKey, grantCommand string
	var jsonOutput bool
	cmd := &cobra.Command{Use: "ingest", Short: "Ingest one externally signed routing decision", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&file, "file", "", "Typed signed-decision JSON file")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Required stable retry key")
	cmd.Flags().StringVar(&grantCommand, "write-grant-command", "", "Command that reads GrantBinding JSON on stdin and prints one city-write token")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(file) == "" || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(grantCommand) == "" {
			fmt.Fprintln(stderr, "gc routing ingest: --file, --idempotency-key, and --write-grant-command are required") //nolint:errcheck
			return errExit
		}
		envelope, err := readRoutingIngestFile(file)
		if err != nil {
			fmt.Fprintf(stderr, "gc routing ingest: signed-decision file rejected: %v\n", err) //nolint:errcheck
			return errExit
		}
		client, err := routingClient(stderr, "ingest")
		if err != nil {
			return err
		}
		if err := client.SetGrantSource(func(binding gcapi.GrantBinding) (string, error) {
			grantCtx, cancel := context.WithTimeout(cmd.Context(), routingGrantTimeout)
			defer cancel()
			return routingGrantCommandRun(grantCtx, grantCommand, binding)
		}); err != nil {
			fmt.Fprintln(stderr, "gc routing ingest: live routing API unavailable") //nolint:errcheck
			return errExit
		}
		result, err := client.RoutingIngest(routingdecision.IngestApprovedRequest{
			Payload: envelope.Payload, Approval: envelope.Approval, Signature: envelope.Signature,
			IdempotencyToken: idempotencyKey,
		})
		if err != nil {
			fmt.Fprintf(stderr, "gc routing ingest: %v\n", err) //nolint:errcheck
			return errExit
		}
		if jsonOutput {
			return writeCLIJSONLine(stdout, result)
		}
		fmt.Fprintf(stdout, "Approved %s (record revision %d, store revision %d)\n", result.Record.Payload.DecisionID, result.Record.RecordRevision, result.Record.StoreRevision) //nolint:errcheck
		return nil
	}
	return cmd
}

func readRoutingIngestFile(path string) (gcapi.RoutingDecisionIngestBody, error) {
	file, err := os.Open(path)
	if err != nil {
		return gcapi.RoutingDecisionIngestBody{}, errors.New("file unavailable")
	}
	defer file.Close() //nolint:errcheck
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > routingIngestFileLimit {
		return gcapi.RoutingDecisionIngestBody{}, errors.New("file must be regular and at most 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, routingIngestFileLimit+1))
	if err != nil || len(data) > routingIngestFileLimit {
		return gcapi.RoutingDecisionIngestBody{}, errors.New("file must be regular and at most 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope gcapi.RoutingDecisionIngestBody
	if err := decoder.Decode(&envelope); err != nil {
		return gcapi.RoutingDecisionIngestBody{}, errors.New("invalid typed JSON")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return gcapi.RoutingDecisionIngestBody{}, errors.New("trailing JSON data")
	}
	return envelope, nil
}

type cappedCommandBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (buffer *cappedCommandBuffer) Write(value []byte) (int, error) {
	if buffer.buffer.Len()+len(value) > buffer.limit {
		return 0, errors.New("command output exceeds limit")
	}
	return buffer.buffer.Write(value)
}

func runRoutingGrantCommand(ctx context.Context, command string, binding gcapi.GrantBinding) (string, error) {
	payload, err := json.Marshal(binding)
	if err != nil {
		return "", errors.New("grant binding unavailable")
	}
	process := exec.CommandContext(ctx, "sh", "-c", strings.TrimSpace(command))
	process.Stdin = bytes.NewReader(payload)
	stdout := &cappedCommandBuffer{limit: routingGrantOutputLimit}
	stderr := &cappedCommandBuffer{limit: routingGrantOutputLimit}
	process.Stdout, process.Stderr = stdout, stderr
	if err := process.Run(); err != nil {
		return "", errors.New("write-grant command failed")
	}
	token := strings.TrimSpace(stdout.buffer.String())
	if err := validateRoutingGrantToken(token); err != nil {
		return "", err
	}
	return token, nil
}

func validateRoutingGrantToken(token string) error {
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("write-grant command returned a malformed token")
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok || payload == "" || signature == "" {
		return errors.New("write-grant command returned a malformed token")
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(payload); err != nil {
		return errors.New("write-grant command returned a malformed token")
	}
	decodedSignature, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil || len(decodedSignature) != ed25519.SignatureSize {
		return errors.New("write-grant command returned a malformed token")
	}
	return nil
}
