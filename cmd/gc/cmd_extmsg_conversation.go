package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/extmsg"
	"github.com/spf13/cobra"
)

// extMsgCommandClientHook keeps conversation commands on the same API route
// as the existing binding commands while giving CLI tests a recording server
// seam. Production always uses extMsgClient.
var extMsgCommandClientHook = extMsgClient

func newExtMsgReplyCmd(stdout, stderr io.Writer) *cobra.Command {
	var conv extMsgConversationFlags
	var sessionID, body, bodyFile, replyToMessageID, idempotencyKey string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "reply",
		Short: "Reply to an external conversation",
		Long: `Reply through the city API with explicit session attribution. When
--conversation-id is omitted, exactly one active conversation binding for
--session (or the current session environment) must exist; ambiguity fails
closed so simultaneous conversations cannot be crossed.

Use --body-file for multiline or sensitive text. --body and --body-file are
mutually exclusive.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runExtMsgReply(conv, sessionID, body, bodyFile, replyToMessageID, idempotencyKey, jsonOutput, stdout, stderr)
		},
	}
	addExtMsgConversationFlags(cmd, &conv)
	cmd.Flags().StringVar(&sessionID, "session", "", "Session selector (default: GC_SESSION_ID, GC_SESSION_NAME, or GC_ALIAS)")
	cmd.Flags().StringVar(&body, "body", "", "Reply body (mutually exclusive with --body-file)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Read the reply body from this file (mutually exclusive with --body)")
	cmd.Flags().StringVar(&replyToMessageID, "reply-to", "", "Provider message ID to reply to")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key for safe retries")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output the API result as JSON")
	return cmd
}

func runExtMsgReply(conv extMsgConversationFlags, sessionID, body, bodyFile, replyToMessageID, idempotencyKey string, jsonOutput bool, stdout, stderr io.Writer) error {
	if bodyFile != "" && body != "" {
		return fmt.Errorf("gc extmsg reply: --body and --body-file are mutually exclusive")
	}
	if bodyFile == "" && body == "" {
		return fmt.Errorf("gc extmsg reply: one of --body or --body-file is required")
	}
	if bodyFile != "" {
		contents, err := os.ReadFile(bodyFile)
		if err != nil {
			return fmt.Errorf("gc extmsg reply: reading --body-file %q: %w", bodyFile, err)
		}
		body = string(contents)
	}

	selector := extMsgSessionSelector(sessionID)
	if selector == "" {
		return fmt.Errorf("gc extmsg reply: --session or GC_SESSION_ID/GC_SESSION_NAME/GC_ALIAS is required")
	}
	c, cityPath, ok := extMsgCommandClientHook("reply", stderr)
	if !ok || c == nil {
		return errExit
	}
	ref, err := resolveExtMsgConversation(c, cityPath, conv, selector)
	if err != nil {
		return fmt.Errorf("gc extmsg reply: %w", err)
	}
	result, err := c.SendExtMsg(api.ExtMsgOutboundSpec{
		Conversation:     ref,
		SessionID:        selector,
		Text:             body,
		ReplyToMessageID: strings.TrimSpace(replyToMessageID),
		IdempotencyKey:   strings.TrimSpace(idempotencyKey),
	})
	if err != nil {
		return extMsgConversationAPIError("reply", c, err)
	}
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return fmt.Errorf("gc extmsg reply: encoding JSON output: %w", err)
		}
		return nil
	}
	fmt.Fprintf(stdout, "replied to %s/%s (message %s; accepted=%t queued=%t delivered=%t)\n", //nolint:errcheck // best-effort stdout
		result.Receipt.Conversation.Provider,
		result.Receipt.Conversation.ConversationID,
		result.Receipt.MessageID,
		result.Receipt.Accepted,
		result.Receipt.Queued,
		result.Receipt.Delivered,
	)
	return nil
}

func newExtMsgTranscriptCmd(stdout, stderr io.Writer) *cobra.Command {
	var conv extMsgConversationFlags
	var sessionID, order string
	var afterSequence int64
	var limit int
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "transcript",
		Short: "Read a scoped external-conversation transcript",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			selector := extMsgSessionSelector(sessionID)
			c, cityPath, ok := extMsgCommandClientHook("transcript", stderr)
			if !ok || c == nil {
				return errExit
			}
			ref, err := resolveExtMsgConversation(c, cityPath, conv, selector)
			if err != nil {
				return fmt.Errorf("gc extmsg transcript: %w", err)
			}
			entries, err := c.ListExtMsgTranscript(api.ExtMsgTranscriptSpec{
				Conversation:  ref,
				AfterSequence: afterSequence,
				Limit:         limit,
				Order:         extmsg.TranscriptOrder(strings.TrimSpace(order)),
			})
			if err != nil {
				return extMsgConversationAPIError("transcript", c, err)
			}
			return printExtMsgTranscript(stdout, jsonOutput, entries)
		},
	}
	addExtMsgConversationFlags(cmd, &conv)
	cmd.Flags().StringVar(&sessionID, "session", "", "Session selector for safe current-conversation selection")
	cmd.Flags().Int64Var(&afterSequence, "after-sequence", 0, "Return entries after this sequence")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum entries to return (server default when omitted)")
	cmd.Flags().StringVar(&order, "order", "", "Transcript order: asc or desc")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output transcript entries as JSON")
	cmd.AddCommand(newExtMsgTranscriptAckCmd(stdout, stderr))
	return cmd
}

func newExtMsgTranscriptAckCmd(stdout, stderr io.Writer) *cobra.Command {
	var conv extMsgConversationFlags
	var sessionID string
	var sequence int64
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "ack",
		Short: "Acknowledge a scoped transcript sequence",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if sequence <= 0 {
				return fmt.Errorf("gc extmsg transcript ack: --sequence must be greater than zero")
			}
			selector := extMsgSessionSelector(sessionID)
			if selector == "" {
				return fmt.Errorf("gc extmsg transcript ack: --session or GC_SESSION_ID/GC_SESSION_NAME/GC_ALIAS is required")
			}
			c, cityPath, ok := extMsgCommandClientHook("transcript ack", stderr)
			if !ok || c == nil {
				return errExit
			}
			ref, err := resolveExtMsgConversation(c, cityPath, conv, selector)
			if err != nil {
				return fmt.Errorf("gc extmsg transcript ack: %w", err)
			}
			if err := c.AckExtMsgTranscript(api.ExtMsgTranscriptAckSpec{Conversation: ref, SessionID: selector, Sequence: sequence}); err != nil {
				return extMsgConversationAPIError("transcript ack", c, err)
			}
			if jsonOutput {
				return json.NewEncoder(stdout).Encode(struct {
					Acknowledged bool   `json:"acknowledged"`
					Conversation string `json:"conversation_id"`
					Sequence     int64  `json:"sequence"`
				}{Acknowledged: true, Conversation: ref.ConversationID, Sequence: sequence})
			}
			fmt.Fprintf(stdout, "acknowledged %s/%s through sequence %d\n", ref.Provider, ref.ConversationID, sequence) //nolint:errcheck // best-effort stdout
			return nil
		},
	}
	addExtMsgConversationFlags(cmd, &conv)
	cmd.Flags().StringVar(&sessionID, "session", "", "Session selector for the acknowledgement")
	cmd.Flags().Int64Var(&sequence, "sequence", 0, "Acknowledge entries through this sequence (required)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output acknowledgement as JSON")
	return cmd
}

func resolveExtMsgConversation(c *api.Client, cityPath string, conv extMsgConversationFlags, sessionSelector string) (extmsg.ConversationRef, error) {
	if strings.TrimSpace(conv.provider) != "" || strings.TrimSpace(conv.conversationID) != "" {
		return conv.conversationRef(cityPath)
	}
	if strings.TrimSpace(sessionSelector) == "" {
		return extmsg.ConversationRef{}, fmt.Errorf("--session or a current session environment is required when --conversation-id is omitted")
	}
	bindings, err := c.ListExtMsgBindings(sessionSelector)
	if err != nil {
		return extmsg.ConversationRef{}, err
	}
	switch len(bindings) {
	case 0:
		return extmsg.ConversationRef{}, fmt.Errorf("no active conversation is bound to session %q; pass --provider and --conversation-id", sessionSelector)
	case 1:
		return bindings[0].Conversation, nil
	default:
		return extmsg.ConversationRef{}, fmt.Errorf("current conversation for session %q is ambiguous (%d active bindings); pass --provider and --conversation-id", sessionSelector, len(bindings))
	}
}

func extMsgSessionSelector(explicit string) string {
	if selector := strings.TrimSpace(explicit); selector != "" {
		return selector
	}
	for _, key := range []string{"GC_SESSION_ID", "GC_SESSION_NAME", "GC_ALIAS"} {
		if selector := strings.TrimSpace(os.Getenv(key)); selector != "" {
			return selector
		}
	}
	return ""
}

func printExtMsgTranscript(stdout io.Writer, jsonOutput bool, entries []extmsg.ConversationTranscriptRecord) error {
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(entries); err != nil {
			return fmt.Errorf("gc extmsg transcript: encoding JSON output: %w", err)
		}
		return nil
	}
	for _, entry := range entries {
		actor := entry.Actor.DisplayName
		if actor == "" {
			actor = entry.Actor.ID
		}
		fmt.Fprintf(stdout, "%d %s %s: %s\n", entry.Sequence, entry.Kind, actor, entry.Text) //nolint:errcheck // best-effort stdout
	}
	return nil
}

func extMsgConversationAPIError(verb string, c *api.Client, err error) error {
	if api.ShouldFallback(c, err) {
		return fmt.Errorf("gc extmsg %s: city API unreachable (conversation commands have no local fallback): %w", verb, err)
	}
	return fmt.Errorf("gc extmsg %s: %w", verb, err)
}
