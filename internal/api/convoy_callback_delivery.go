package api

import (
	"context"
	"log"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/convoycallback"
	"github.com/gastownhall/gascity/internal/convoydelivery"
)

func (s *Server) deliverConvoyLifecycleCallback(ctx context.Context, store beads.Store, convoy beads.Bead, kind convoycallback.EventType) {
	cfg := s.state.Config()
	if cfg == nil || cfg.ExternalCoordination == nil || store == nil {
		return
	}
	if _, err := convoydelivery.NewService(store, s.state.CityName(), cfg.ExternalCoordination).Deliver(ctx, convoy, kind); err != nil {
		log.Printf("gc api: convoy callback %s for %s: %v", kind, convoy.ID, err)
	}
}
