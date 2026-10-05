package main

import (
	"context"
	"fmt"
	"io"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/convoycallback"
	"github.com/gastownhall/gascity/internal/convoydelivery"
	"github.com/gastownhall/gascity/internal/events"
)

// recordConvoyLifecycleEvent keeps the existing event journal as the
// authoritative lifecycle observation and composes the callback path after
// the convoy mutation has succeeded. Callback admission/queue failures are
// warnings because they cannot safely roll back a committed convoy transition;
// the durable callback records remain available for reconciliation.
func recordConvoyLifecycleEvent(
	rec events.Recorder,
	store beads.Store,
	convoy beads.Bead,
	kind convoycallback.EventType,
	message string,
	cfg *config.City,
	cityName string,
	stderr io.Writer,
) {
	rec.Record(events.Event{
		Type:    string(kind),
		Actor:   eventActor(),
		Subject: convoy.ID,
		Message: message,
	})
	if cfg == nil || cfg.ExternalCoordination == nil || store == nil {
		return
	}
	result, err := convoydelivery.NewService(store, cityName, cfg.ExternalCoordination).Deliver(context.Background(), convoy, kind)
	if err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "gc convoy callback: %s: %v\n", kind, err) //nolint:errcheck // best-effort diagnostic
		}
		return
	}
	if result.Skipped {
		return
	}
}

func convoyLifecycleCityName(cfg *config.City, cityPath string) string {
	if cfg == nil {
		return ""
	}
	return loadedCityName(cfg, cityPath)
}
