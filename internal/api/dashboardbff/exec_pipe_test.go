package dashboardbff

import (
	"context"
	"testing"
	"time"
)

func TestExecRunnerCancellationDoesNotWaitForInheritedOutputPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	start := time.Now()
	go func() {
		_, _ = newExecRunner().run(ctx, "sh", []string{"-c", "sleep 30 & wait"}, time.Minute)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("cancellation took %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after cancellation")
	}
}
