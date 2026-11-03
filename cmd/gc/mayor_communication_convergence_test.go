package main

import (
	"bytes"
	"testing"
)

// TestMayorCommunicationConversationCommandIsRootRegistered is the integrated
// regression for the user-facing projection. Package-local command tests do
// not prove that the root CLI exposes the conversation reply surface.
func TestMayorCommunicationConversationCommandIsRootRegistered(t *testing.T) {
	root := newRootCmd(&bytes.Buffer{}, &bytes.Buffer{})

	command, remaining, err := root.Find([]string{"extmsg", "reply"})
	if err != nil {
		t.Fatalf("find extmsg reply: %v", err)
	}
	if command == nil || len(remaining) != 0 {
		t.Fatalf("root.Find(extmsg reply) = command %v, remaining %v; want registered leaf", command, remaining)
	}
}
