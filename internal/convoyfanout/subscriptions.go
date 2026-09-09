package convoyfanout

import (
	"github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/convoycallback"
)

// SubscriptionViews projects durable callback subscriptions into the
// authorization evidence fan-out consumes. It copies every slice so a caller
// mutating its records cannot reach into an admission decision.
//
// Only the subscription state decides Active; a lifecycle interest the callback
// contract does not define is carried through unchanged and simply matches no
// event. Route identity stays opaque here as everywhere else.
func SubscriptionViews(records []convoy.SubscriptionRecord) []SubscriptionView {
	views := make([]SubscriptionView, 0, len(records))
	for _, record := range records {
		views = append(views, SubscriptionView{
			ID:             record.ID,
			Owner:          record.Owner,
			RegistrationID: record.RegistrationID,
			Generation:     record.Generation,
			Active:         record.State == convoy.SubscriptionActive,
			Scope: WorkScope{
				City:     record.Scope.City,
				ConvoyID: record.Scope.ConvoyID,
				WorkRefs: append([]string(nil), record.Scope.WorkRefs...),
			},
			Interests:     lifecycleInterests(record.Interests),
			RouteIdentity: record.RouteIdentity,
		})
	}
	return views
}

func lifecycleInterests(interests []convoy.LifecycleInterest) []convoycallback.EventType {
	if len(interests) == 0 {
		return nil
	}
	out := make([]convoycallback.EventType, len(interests))
	for i, interest := range interests {
		out[i] = convoycallback.EventType(interest)
	}
	return out
}
