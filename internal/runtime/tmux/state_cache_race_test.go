package tmux

import (
	"sync"
	"testing"
	"time"
)

func primedStateCacheForEvictionTest() *StateCache {
	cache := NewStateCache(&mockFetcher{sessions: map[string]bool{}}, time.Hour)
	cache.state = runtimeStateSnapshot{
		Sessions: map[string]sessionRuntimeState{
			"agent-1": {
				Running: true,
				Panes:   []paneRuntimeState{{Command: "claude", PID: "101"}},
			},
		},
		Processes: newProcessSnapshot([]processRuntimeState{{
			PID:     "101",
			PPID:    "1",
			Command: "claude",
			Args:    "claude",
		}}),
		ProcessesAvailable: true,
	}
	cache.fetchedAt = time.Now()
	return cache
}

func TestStateCache_EvictSessionDoesNotMutatePublishedSnapshot(t *testing.T) {
	cache := primedStateCacheForEvictionTest()
	published := cache.currentState()

	cache.EvictSession("agent-1")

	if session, ok := published.Sessions["agent-1"]; !ok || !session.Running {
		t.Fatal("EvictSession mutated a previously published runtime snapshot")
	}
}

func TestStateCache_ConcurrentEvictSessionAndReaders(t *testing.T) {
	const (
		attempts = 100
		readers  = 16
		reads    = 100
	)

	for attempt := 0; attempt < attempts; attempt++ {
		cache := primedStateCacheForEvictionTest()
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(readers + 1)

		for reader := 0; reader < readers; reader++ {
			go func() {
				defer wg.Done()
				<-start
				for range reads {
					_ = cache.IsRunning("agent-1")
					_ = cache.ProcessAlive("agent-1", []string{"claude"})
				}
			}()
		}
		go func() {
			defer wg.Done()
			<-start
			cache.EvictSession("agent-1")
		}()

		close(start)
		wg.Wait()

		if cache.IsRunning("agent-1") {
			t.Fatalf("attempt %d: evicted session reported running", attempt)
		}
		if cache.ProcessAlive("agent-1", []string{"claude"}) {
			t.Fatalf("attempt %d: evicted session process reported alive", attempt)
		}
	}
}
