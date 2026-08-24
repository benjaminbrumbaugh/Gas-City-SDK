package api

import (
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/routingdecision"
)

// outcomeSafeIdentifierPattern is the portable routing/outcome/v2 identifier
// grammar. IDs begin with an ASCII alphanumeric. Colons and slashes remain
// available for opaque namespaced IDs, but no colon-delimited segment may begin
// with //, so URL-shaped values fail at generated-client boundaries.
const outcomeSafeIdentifierPattern = `^[A-Za-z0-9][A-Za-z0-9._/-]*(?::(?:/?[A-Za-z0-9._-][A-Za-z0-9._/-]*|/?))*$`

const outcomeCredentialCharacters = `[A-Za-z0-9_-]`

type (
	outcomeRecordSchema       struct{}
	outcomeRecordSchemaFields routingdecision.OutcomeRecord
)

type outcomeSecretShape struct {
	prefix                 string
	caseInsensitivePrefix  bool
	minimumSuffix          int
	maximumSuffix          int
	suffixCharacterPattern string
}

func (outcomeRecordSchema) Schema(r huma.Registry) *huma.Schema {
	const name = "OutcomeRecord"
	if _, ok := r.Map()[name]; !ok {
		fields := huma.SchemaFromType(r, reflect.TypeOf(outcomeRecordSchemaFields{}))
		constrainOutcomeSafeIdentifiers(fields)
		schema := &huma.Schema{AllOf: []*huma.Schema{
			fields,
			outcomeRecordStateMachineConstraint(),
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
		base := outcomeStringSchema(property.Nullable, outcomeSafeIdentifierPattern, 1, 256)
		constraints := append([]*huma.Schema{base}, outcomeSecretSafetyConstraints(property.Nullable)...)
		property.AllOf = outcomeBinaryAllOf(constraints)
	}
}

func outcomeBinaryAllOf(schemas []*huma.Schema) []*huma.Schema {
	if len(schemas) <= 2 {
		return schemas
	}
	return []*huma.Schema{schemas[0], {AllOf: outcomeBinaryAllOf(schemas[1:])}}
}

// outcomeSecretSafetyConstraints is the positive, generator-safe complement of
// resemblesOutcomeSecret. OpenAPI `not` is understood by Huma but dropped by
// the pinned @hey-api Zod generator, so each credential shape is represented as
// an allOf of allowed alternatives: too short, too long, a different prefix, or
// a suffix outside the credential alphabet. Both Go's RE2 and JavaScript's
// RegExp consume these patterns identically.
func outcomeSecretSafetyConstraints(nullable bool) []*huma.Schema {
	shapes := []outcomeSecretShape{
		// Frozen Wayfinder safeString rule: sk- is rejected unconditionally,
		// so the wire grammar accepts no sk- prefixed value at all. The empty
		// mismatch pattern can never match, making this complement impossible.
		{prefix: "sk-", caseInsensitivePrefix: true, minimumSuffix: 16, maximumSuffix: 253, suffixCharacterPattern: `[a^]`},
		{prefix: "rk-", caseInsensitivePrefix: true, minimumSuffix: 16, maximumSuffix: 253, suffixCharacterPattern: outcomeCredentialCharacters},
		{prefix: "ghp_", caseInsensitivePrefix: true, minimumSuffix: 36, maximumSuffix: 36, suffixCharacterPattern: outcomeCredentialCharacters},
		{prefix: "gho_", caseInsensitivePrefix: true, minimumSuffix: 36, maximumSuffix: 36, suffixCharacterPattern: outcomeCredentialCharacters},
		{prefix: "github_pat_", caseInsensitivePrefix: true, minimumSuffix: 82, maximumSuffix: 82, suffixCharacterPattern: outcomeCredentialCharacters},
		{prefix: "xoxb-", caseInsensitivePrefix: true, minimumSuffix: 20, maximumSuffix: 251, suffixCharacterPattern: outcomeCredentialCharacters},
		{prefix: "xoxp-", caseInsensitivePrefix: true, minimumSuffix: 20, maximumSuffix: 251, suffixCharacterPattern: outcomeCredentialCharacters},
		{prefix: "bearer-", caseInsensitivePrefix: true, minimumSuffix: 16, maximumSuffix: 249, suffixCharacterPattern: outcomeCredentialCharacters},
		{prefix: "basic-", caseInsensitivePrefix: true, minimumSuffix: 16, maximumSuffix: 250, suffixCharacterPattern: outcomeCredentialCharacters},
		{prefix: "AKIA", minimumSuffix: 16, maximumSuffix: 16, suffixCharacterPattern: `[A-Z0-9]`},
		{prefix: "ASIA", minimumSuffix: 16, maximumSuffix: 16, suffixCharacterPattern: `[A-Z0-9]`},
		{prefix: "AIza", minimumSuffix: 35, maximumSuffix: 35, suffixCharacterPattern: outcomeCredentialCharacters},
	}
	constraints := make([]*huma.Schema, 0, len(shapes)+1)
	for _, shape := range shapes {
		constraints = append(constraints, outcomeSecretShapeComplement(shape, nullable))
	}
	constraints = append(constraints, outcomeJWTComplement(nullable))
	return constraints
}

func outcomeSecretShapeComplement(shape outcomeSecretShape, nullable bool) *huma.Schema {
	prefixPattern, prefixMismatch := outcomePrefixPatterns(shape.prefix, shape.caseInsensitivePrefix)
	if shape.suffixCharacterPattern == "[a^]" {
		// Frozen Wayfinder rule: every sk- prefixed identifier is rejected
		// unconditionally, so the accepting set is exactly the prefix
		// mismatch — any value whose head is not sk- in any casing.
		return outcomeStringSchema(nullable, prefixMismatch, 0, 0)
	}
	minimumLength := len(shape.prefix) + shape.minimumSuffix
	maximumLength := len(shape.prefix) + shape.maximumSuffix
	alternatives := []*huma.Schema{
		outcomeStringSchema(nullable, "", 0, minimumLength-1),
		outcomeStringSchema(nullable, prefixMismatch, 0, 0),
		outcomeStringSchema(nullable, "^"+prefixPattern+shape.suffixCharacterPattern+"*[^"+strings.Trim(shape.suffixCharacterPattern, "[]")+"]", 0, 0),
	}
	if maximumLength < 256 {
		alternatives = append(alternatives, outcomeStringSchema(nullable, "", maximumLength+1, 0))
	}
	return &huma.Schema{AnyOf: alternatives}
}

func outcomePrefixPatterns(prefix string, foldCase bool) (string, string) {
	parts := make([]string, 0, len(prefix))
	mismatches := make([]string, 0, len(prefix))
	for index := 0; index < len(prefix); index++ {
		char := prefix[index]
		exact, mismatch := string(char), "[^"+string(char)+"]"
		if foldCase && char >= 'a' && char <= 'z' {
			exact = "[" + string(char) + string(char-'a'+'A') + "]"
			mismatch = "[^" + string(char) + string(char-'a'+'A') + "]"
		}
		mismatches = append(mismatches, strings.Join(parts, "")+mismatch)
		parts = append(parts, exact)
	}
	return strings.Join(parts, ""), "^(?:" + strings.Join(mismatches, "|") + ")"
}

// outcomeJWTComplement accepts exactly the identifiers that do not have the
// three credential-alphabet segments recognized by resemblesOutcomeSecret.
func outcomeJWTComplement(nullable bool) *huma.Schema {
	patterns := []string{
		`^[^.]*$`,
		`^[^.]*\.[^.]*$`,
		`^(?:[^.]*\.){3}`,
		`^[^.]{0,7}\.`,
		`^(?:[^e]|e[^y]|ey[^J])[^.]*\.`,
		`^[^.]*[^A-Za-z0-9_.-][^.]*\.`,
		`^[^.]*\.[^.]{0,7}\.`,
		`^[^.]*\.(?:[^e]|e[^y]|ey[^J])[^.]*\.`,
		`^[^.]*\.[^.]*[^A-Za-z0-9_.-][^.]*\.`,
		`^[^.]*\.[^.]*\.[^.]{0,7}$`,
		`^[^.]*\.[^.]*\.[^.]*[^A-Za-z0-9_.-][^.]*$`,
	}
	alternatives := make([]*huma.Schema, 0, len(patterns))
	for _, pattern := range patterns {
		alternatives = append(alternatives, outcomeStringSchema(nullable, pattern, 0, 0))
	}
	return &huma.Schema{AnyOf: alternatives}
}

func outcomeStringSchema(nullable bool, pattern string, minimumLength, maximumLength int) *huma.Schema {
	schema := &huma.Schema{Type: huma.TypeString, Nullable: nullable, Pattern: pattern}
	if minimumLength > 0 {
		schema.MinLength = intPointer(minimumLength)
	}
	if maximumLength > 0 {
		schema.MaxLength = intPointer(maximumLength)
	}
	return schema
}

func outcomeRecordStateMachineConstraint() *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		outcomeStateSchema("claimed", []string{"unknown"}, []string{"unknown"}, true, false, false),
		outcomeStateSchema("succeeded", []string{"shipped", "no_op"}, []string{"none"}, true, true, false),
		outcomeStateSchema("failed", []string{"not_admitted"}, []string{"transient", "hard", "unknown"}, false, false, true),
		outcomeStateSchema("failed", []string{"blocked", "abandoned", "unknown"}, []string{"transient", "hard", "unknown"}, true, false, false),
	}}
}

func outcomeStateSchema(status string, dispositions, failures []string, actualsNonNull, causalNonNull, causalNull bool) *huma.Schema {
	properties := map[string]*huma.Schema{
		"status":        stringEnumSchema(status),
		"disposition":   stringEnumSchema(dispositions...),
		"failure_class": stringEnumSchema(failures...),
	}
	required := []string{"status", "disposition", "failure_class"}
	if actualsNonNull {
		properties["actual_target_id"] = &huma.Schema{Type: huma.TypeString}
		properties["actual_config_digest"] = &huma.Schema{Type: huma.TypeString}
		required = append(required, "actual_target_id", "actual_config_digest")
	} else {
		properties["actual_target_id"] = nullOnlySchema()
		properties["actual_config_digest"] = nullOnlySchema()
		required = append(required, "actual_target_id", "actual_config_digest")
	}
	if causalNonNull || causalNull {
		for _, field := range []string{"admission_receipt_id", "session_id", "execution_id"} {
			if causalNonNull {
				properties[field] = &huma.Schema{Type: huma.TypeString}
			} else {
				properties[field] = nullOnlySchema()
			}
			required = append(required, field)
		}
	}
	return &huma.Schema{Type: huma.TypeObject, Properties: properties, Required: required}
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
