package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/spf13/cobra"
)

// newHCACmd exposes the Mayor's provider-neutral Human Coordinator Agent
// affordance. Every operation goes through the city API; there is no local
// fallback that could race the controller's durable queue.
func newHCACmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hca",
		Short: "Use the Human Coordinator Agent affordance",
		Long:  "Discover and enqueue causally linked requests for the configured external Human Coordinator Agent.",
	}
	cmd.AddCommand(newHCAShowCmd(stdout, stderr))
	cmd.AddCommand(newHCARequestCmd(stdout, stderr))
	cmd.AddCommand(newHCAListCmd(stdout, stderr))
	return cmd
}

func newHCAShowCmd(stdout, _ io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the configured HCA signifier",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cityPath, err := resolveCity()
			if err != nil {
				return err
			}
			client, reason := maintenanceAPIClient(cityPath)
			if client == nil {
				return fmt.Errorf("city API unavailable: %s", reason)
			}
			signifier, err := client.GetHCAAffordance()
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(stdout).Encode(signifier)
			}
			_, _ = fmt.Fprintf(stdout, "available: %t\nrole: %s\ntarget: %s\nadapter: %s\nconfig revision: %d\ndelivery: %s\ninterrupt policy: %s\nsession policy: %s\n", signifier.Available, signifier.LogicalRole, signifier.Target, signifier.Adapter, signifier.ConfigRevision, signifier.Delivery, signifier.InterruptPolicy, signifier.SessionPolicy)
			if signifier.Triggers != nil {
				_, _ = fmt.Fprintf(stdout, "triggers: %s\n", strings.Join(*signifier.Triggers, ", "))
			}
			_, _ = fmt.Fprintf(stdout, "instruction: %s\n", signifier.Instruction)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output JSON")
	return cmd
}

type hcaRequestFlags struct {
	sourceAgent       string
	reason            string
	prompt            string
	workRef           string
	repository        string
	correlationID     string
	idempotencyKey    string
	contentRetention  string
	resultDestination string
	allowedTools      []string
}

func newHCARequestCmd(stdout, _ io.Writer) *cobra.Command {
	flags := &hcaRequestFlags{}
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Queue a request for the HCA",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(flags.sourceAgent) == "" {
				flags.sourceAgent = strings.TrimSpace(os.Getenv("GC_AGENT"))
			}
			if flags.sourceAgent == "" {
				return fmt.Errorf("--source-agent is required when GC_AGENT is unset")
			}
			if strings.TrimSpace(flags.prompt) == "" {
				return fmt.Errorf("--prompt is required")
			}
			if strings.TrimSpace(flags.idempotencyKey) == "" {
				return fmt.Errorf("--idempotency-key is required for safe retries")
			}
			cityPath, err := resolveCity()
			if err != nil {
				return err
			}
			client, reason := maintenanceAPIClient(cityPath)
			if client == nil {
				return fmt.Errorf("city API unavailable: %s", reason)
			}
			body := genclient.HCARequestBody{
				SourceAgent:      flags.sourceAgent,
				Prompt:           flags.prompt,
				Reason:           flags.reason,
				ContentRetention: stringPtr(flags.contentRetention),
				IdempotencyKey:   stringPtr(flags.idempotencyKey),
			}
			setOptionalString(&body.WorkRef, flags.workRef)
			setOptionalString(&body.Repository, flags.repository)
			setOptionalString(&body.Rig, rigFlag)
			setOptionalString(&body.CorrelationId, flags.correlationID)
			setOptionalString(&body.ResultDestination, flags.resultDestination)
			if len(flags.allowedTools) > 0 {
				body.AllowedTools = &flags.allowedTools
			}
			record, err := client.EnqueueHCARequest(body)
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(stdout).Encode(record)
			}
			_, _ = fmt.Fprintf(stdout, "request queued: %s\nstate: %s\nwork_ref: %s\ncorrelation_id: %s\n", record.Id, record.State, hcaOptionalString(record.Request.WorkRef), record.Request.CorrelationId)
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.sourceAgent, "source-agent", "", "Authenticated orchestrator identity (default: GC_AGENT)")
	cmd.Flags().StringVar(&flags.reason, "reason", "escalation", "Why the HCA is needed")
	cmd.Flags().StringVar(&flags.prompt, "prompt", "", "Decision or assistance request for the HCA")
	cmd.Flags().StringVar(&flags.workRef, "work-ref", "", "Bead or work reference")
	cmd.Flags().StringVar(&flags.repository, "repository", "", "Repository scope")
	cmd.Flags().StringVar(&flags.correlationID, "correlation-id", "", "Stable causal correlation ID")
	cmd.Flags().StringVar(&flags.idempotencyKey, "idempotency-key", "", "Stable retry key (required)")
	cmd.Flags().StringVar(&flags.contentRetention, "content-retention", "ephemeral", "Content retention: ephemeral or durable")
	cmd.Flags().StringVar(&flags.resultDestination, "result-destination", "", "Where the correlated response should be recorded")
	cmd.Flags().StringSliceVar(&flags.allowedTools, "allowed-tool", nil, "Tool explicitly allowed to the HCA (repeatable)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output JSON")
	_ = cmd.MarkFlagRequired("prompt")
	_ = cmd.MarkFlagRequired("idempotency-key")
	return cmd
}

func newHCAListCmd(stdout, _ io.Writer) *cobra.Command {
	var state string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List durable HCA requests",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cityPath, err := resolveCity()
			if err != nil {
				return err
			}
			client, reason := maintenanceAPIClient(cityPath)
			if client == nil {
				return fmt.Errorf("city API unavailable: %s", reason)
			}
			items, err := client.ListHCARequests(state)
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(stdout).Encode(items)
			}
			for _, item := range items {
				_, _ = fmt.Fprintf(stdout, "%s	%s	%s	%s\n", item.Id, item.State, item.Request.Reason, hcaOptionalString(item.Request.WorkRef))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "Filter by delivery state")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output JSON")
	return cmd
}

func setOptionalString(destination **string, value string) {
	if strings.TrimSpace(value) != "" {
		*destination = stringPtr(value)
	}
}

func hcaOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
