package api

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

// outcomeSafeIdentifierPattern is the portable routing/outcome/v2 identifier
// grammar. Colons and slashes remain available for opaque namespaced IDs, but
// no colon-delimited segment may begin with //, so URL-shaped values fail at
// generated-client boundaries instead of relying only on server-side checks.
const outcomeSafeIdentifierPattern = `^[A-Za-z0-9][A-Za-z0-9._/-]*(?::(?:/?[A-Za-z0-9._-][A-Za-z0-9._/-]*|/?))*$`

type outcomeRecordSchema struct{}
type outcomeRecordSchemaFields routingdecision.OutcomeRecord

func (outcomeRecordSchema) Schema(r huma.Registry) *huma.Schema {
	const name = "OutcomeRecord"
	if _, ok := r.Map()[name]; !ok {
		fields := huma.SchemaFromType(r, reflect.TypeOf(outcomeRecordSchemaFields{}))
		constrainOutcomeSafeIdentifiers(fields)
		schema := &huma.Schema{AllOf: []*huma.Schema{
			fields,
			{AllOf: []*huma.Schema{
				outcomeSucceededEvidenceConstraint(),
				outcomeNotAdmittedActualsConstraint(),
			}},
		}, Extensions: map[string]any{
			"x-go-type": "routingdecision.OutcomeRecord",
			"x-go-type-import": map[string]any{
				"path": "github.com/gastownhall/gascity/internal/routingdecision",
			},
		}}
		schema.PrecomputeMessages()
		r.Map()[name] = schema
	}
	return &huma.Schema{Ref: schemaRefPrefix + name}
}

func constrainOutcomeSafeIdentifiers(schema *huma.Schema) {
	for _, field := range []string{
		"correlation_id",
		"routing_decision_id",
		"work_id",
		"admission_receipt_id",
		"session_id",
		"execution_id",
		"requested_target_id",
		"actual_target_id",
	} {
		property := schema.Properties[field]
		property.Pattern = outcomeSafeIdentifierPattern
		property.MinLength = intPointer(1)
		property.MaxLength = intPointer(256)
	}
}

func outcomeSucceededEvidenceConstraint() *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		{
			Type: "object",
			Properties: map[string]*huma.Schema{
				"status":               stringEnumSchema("succeeded"),
				"admission_receipt_id": {Type: huma.TypeString},
				"session_id":           {Type: huma.TypeString},
				"execution_id":         {Type: huma.TypeString},
			},
			Required: []string{"status", "admission_receipt_id", "session_id", "execution_id"},
		},
		{
			Type: "object",
			Properties: map[string]*huma.Schema{
				"status": stringEnumSchema("claimed", "failed"),
			},
			Required: []string{"status"},
		},
	}}
}

func outcomeNotAdmittedActualsConstraint() *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		{
			Type: "object",
			Properties: map[string]*huma.Schema{
				"disposition":          stringEnumSchema("not_admitted"),
				"actual_target_id":     nullOnlySchema(),
				"actual_config_digest": nullOnlySchema(),
			},
			Required: []string{"disposition", "actual_target_id", "actual_config_digest"},
		},
		{
			Type: "object",
			Properties: map[string]*huma.Schema{
				"disposition": stringEnumSchema("shipped", "no_op", "blocked", "abandoned", "unknown"),
			},
			Required: []string{"disposition"},
		},
	}}
}

func stringEnumSchema(values ...string) *huma.Schema {
	enum := make([]any, len(values))
	for index, value := range values {
		enum[index] = value
	}
	return &huma.Schema{Type: huma.TypeString, Enum: enum}
}

// nullOnlySchema uses a nullable string with an impossible RE2/ECMAScript
// pattern. Huma's schema model and the dashboard generator both widen a bare
// JSON Schema `type: null` to unknown/null; this supported shape instead emits
// `z.string().regex(/a^/).nullable()`, which accepts exactly null while keeping
// the generated Go model field-addressable.
func nullOnlySchema() *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Nullable: true, Pattern: "a^"}
}

func intPointer(value int) *int { return &value }
