package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	wayfinderEvaluatePath       = "/routing/v3/evaluate"
	wayfinderRequestHeaderValue = "operator-v1"
	maximumWayfinderPacketBytes = 1 << 20
	maximumWayfinderRecords     = 128
	maximumWayfinderTools       = 16
)

const (
	wayfinderDomainRequest         = "routing/v3/request"
	wayfinderDomainInventory       = "routing/v3/inventory"
	wayfinderDomainModelAssessment = "routing/v3/model_assessment"
	wayfinderDomainAccountScope    = "routing/v3/account_scope"
	wayfinderDomainEvidence        = "routing/v3/evidence"
	wayfinderDomainPolicy          = "routing/v3/policy"
	wayfinderDomainClock           = "routing/v3/clock"
	wayfinderDomainDecision        = "routing/v3/decision"
)

// WayfinderRecoveryAdvisor asks the loopback Wayfinder routing/v3 evaluate
// endpoint for advisory routing. Its typed template is the outbound trust
// boundary: only routing/v3's closed records and opaque safe identifiers cross.
type WayfinderRecoveryAdvisor struct {
	endpoint string
	template wayfinderEvaluateRequest
	client   *http.Client
	now      func() time.Time
}

type wayfinderEvaluateRequest struct {
	SchemaVersion    string                     `json:"schema_version"`
	CorrelationID    string                     `json:"correlation_id"`
	Workload         wayfinderWorkloadProfile   `json:"workload"`
	Constraints      wayfinderConstraints       `json:"constraints"`
	Objectives       wayfinderObjectives        `json:"objectives"`
	Preferences      wayfinderPreferences       `json:"preferences"`
	Candidates       []wayfinderCandidate       `json:"candidates"`
	ModelAssessments []wayfinderModelAssessment `json:"model_assessments"`
	Entitlements     []wayfinderEntitlement     `json:"entitlements"`
	Observations     []wayfinderObservation     `json:"observations"`
	NowUnix          int64                      `json:"now_unix"`
	PolicyVersion    string                     `json:"policy_version"`
}

type wayfinderWorkloadProfile struct {
	ProfileSource            string   `json:"profile_source"`
	Operation                string   `json:"operation"`
	Artifact                 string   `json:"artifact"`
	Complexity               string   `json:"complexity"`
	Consequence              string   `json:"consequence"`
	ExecutionShape           string   `json:"execution_shape"`
	ReasoningRequirement     string   `json:"reasoning_requirement"`
	MinimumContextTokens     uint32   `json:"minimum_context_tokens"`
	ExpectedInputTokens      uint32   `json:"expected_input_tokens"`
	ExpectedOutputTokens     uint32   `json:"expected_output_tokens"`
	RequiredTools            []string `json:"required_tools"`
	StructuredOutput         string   `json:"structured_output"`
	RequiredInputModalities  []string `json:"required_input_modalities"`
	RequiredOutputModalities []string `json:"required_output_modalities"`
}

type wayfinderConstraints struct {
	Policy wayfinderPolicyConstraints `json:"policy"`
	Work   wayfinderWorkConstraints   `json:"work"`
}

type wayfinderPolicyConstraints struct {
	DataHandling wayfinderDataHandlingConstraint `json:"data_handling"`
	Residency    wayfinderResidencyConstraint    `json:"residency"`
	Providers    wayfinderProviderConstraint     `json:"providers"`
	Spend        wayfinderSpendConstraint        `json:"spend"`
}

type wayfinderDataHandlingConstraint struct {
	RemoteDisclosure string `json:"remote_disclosure"`
}

type wayfinderResidencyConstraint struct {
	AllowedRegions *[]string `json:"allowed_regions"`
}

type wayfinderProviderConstraint struct {
	AllowedProviderIDs *[]string `json:"allowed_provider_ids"`
	DeniedProviderIDs  []string  `json:"denied_provider_ids"`
}

type wayfinderSpendConstraint struct {
	MaxCostMicros *uint64 `json:"max_cost_micros"`
	Currency      *string `json:"currency"`
}

type wayfinderWorkConstraints struct {
	AllowedTargetIDs []string `json:"allowed_target_ids"`
	ModelID          *string  `json:"model_id"`
	TargetID         *string  `json:"target_id"`
}

type wayfinderObjectives struct {
	Quality uint32 `json:"quality"`
	Latency uint32 `json:"latency"`
	Cost    uint32 `json:"cost"`
}

type wayfinderPreferences struct {
	PreferredProviderID *string `json:"preferred_provider_id"`
	PreferredModelID    *string `json:"preferred_model_id"`
}

type wayfinderCandidate struct {
	CandidateID        string                   `json:"candidate_id"`
	Model              wayfinderModelRecord     `json:"model"`
	ExecutionTarget    wayfinderExecutionTarget `json:"execution_target"`
	Economics          wayfinderEconomics       `json:"economics"`
	ModelAssessmentRef *string                  `json:"model_assessment_ref"`
	EntitlementRef     string                   `json:"entitlement_ref"`
	ObservationRef     string                   `json:"observation_ref"`
}

type wayfinderModelRecord struct {
	CanonicalModel           string   `json:"canonical_model"`
	ServeAs                  string   `json:"serve_as"`
	VariantRef               string   `json:"variant_ref"`
	ReasoningEffort          string   `json:"reasoning_effort"`
	InputModalities          []string `json:"input_modalities"`
	OutputModalities         []string `json:"output_modalities"`
	MaxContextTokens         uint32   `json:"max_context_tokens"`
	MaxOutputTokens          uint32   `json:"max_output_tokens"`
	SupportsTools            bool     `json:"supports_tools"`
	SupportsStructuredOutput bool     `json:"supports_structured_output"`
	SupportsStreaming        bool     `json:"supports_streaming"`
}

type wayfinderExecutionTarget struct {
	TargetID                   string                     `json:"target_id"`
	FabricKind                 string                     `json:"fabric_kind"`
	ProviderID                 string                     `json:"provider_id"`
	AccountRef                 string                     `json:"account_ref"`
	AdmissionModel             string                     `json:"admission_model"`
	ActivationModel            string                     `json:"activation_model"`
	CapacityModel              string                     `json:"capacity_model"`
	DeploymentMaxContextTokens uint32                     `json:"deployment_max_context_tokens"`
	DeploymentMaxOutputTokens  uint32                     `json:"deployment_max_output_tokens"`
	Residency                  string                     `json:"residency"`
	DataHandling               string                     `json:"data_handling"`
	Isolation                  string                     `json:"isolation"`
	ResourceProfile            *wayfinderResourceProfile  `json:"resource_profile"`
	ConfigDigest               string                     `json:"config_digest"`
	AdapterID                  string                     `json:"adapter_id"`
	AdapterDigest              string                     `json:"adapter_digest"`
	InvocationBinding          wayfinderInvocationBinding `json:"invocation_binding"`
}

type wayfinderResourceProfile struct {
	Scheduler          string  `json:"scheduler"`
	QueueRef           string  `json:"queue_ref"`
	QoSRef             *string `json:"qos_ref"`
	NodesPerAllocation uint32  `json:"nodes_per_allocation"`
	GPUsPerNode        uint32  `json:"gpus_per_node"`
	GPUModel           *string `json:"gpu_model"`
	CPUCoresPerNode    uint32  `json:"cpu_cores_per_node"`
	MemoryGiBPerNode   uint32  `json:"memory_gib_per_node"`
	Interconnect       string  `json:"interconnect"`
	MaxWalltimeSeconds uint32  `json:"max_walltime_seconds"`
}

type wayfinderInvocationBinding struct {
	BindingKind   string `json:"binding_kind"`
	BindingRef    string `json:"binding_ref"`
	AdapterID     string `json:"adapter_id"`
	AdapterDigest string `json:"adapter_digest"`
}

type wayfinderEconomics struct {
	PricingModel      string  `json:"pricing_model"`
	Currency          *string `json:"currency"`
	InputPriceMicros  *uint64 `json:"input_price_micros"`
	OutputPriceMicros *uint64 `json:"output_price_micros"`
	UnitPriceMicros   *uint64 `json:"unit_price_micros"`
	AllocationUnit    *string `json:"allocation_unit"`
	PriceBasis        string  `json:"price_basis"`
	Volatility        string  `json:"volatility"`
}

type wayfinderModelAssessment struct {
	RecordID         string                      `json:"record_id"`
	CanonicalModel   string                      `json:"canonical_model"`
	VariantRef       string                      `json:"variant_ref"`
	OperationFitness []wayfinderOperationFitness `json:"operation_fitness"`
	AssessedAtUnix   int64                       `json:"assessed_at_unix"`
	ExpiresAtUnix    int64                       `json:"expires_at_unix"`
	Provenance       string                      `json:"provenance"`
}

type wayfinderOperationFitness struct {
	Operation string `json:"operation"`
	Fitness   uint32 `json:"fitness"`
}

type wayfinderEntitlement struct {
	RecordID           string                      `json:"record_id"`
	AccountRef         string                      `json:"account_ref"`
	Enabled            bool                        `json:"enabled"`
	AuthorizationState string                      `json:"authorization_state"`
	BillingMode        string                      `json:"billing_mode"`
	AllocationBalance  *wayfinderAllocationBalance `json:"allocation_balance"`
	ObservedAtUnix     int64                       `json:"observed_at_unix"`
	ExpiresAtUnix      int64                       `json:"expires_at_unix"`
	Provenance         string                      `json:"provenance"`
}

type wayfinderAllocationBalance struct {
	Unit      string               `json:"unit"`
	Remaining wayfinderKnownUint64 `json:"remaining"`
}

type wayfinderKnownBool struct {
	Known bool  `json:"known"`
	Value *bool `json:"value,omitempty"`
}

type wayfinderKnownString struct {
	Known bool    `json:"known"`
	Value *string `json:"value,omitempty"`
}

type wayfinderKnownUint64 struct {
	Known bool    `json:"known"`
	Value *uint64 `json:"value,omitempty"`
}

type wayfinderCapacity struct {
	Model                      string                `json:"model"`
	FreeSlots                  *wayfinderKnownUint64 `json:"free_slots,omitempty"`
	TotalSlots                 *wayfinderKnownUint64 `json:"total_slots,omitempty"`
	RemainingTokensPerMinute   *wayfinderKnownUint64 `json:"remaining_tokens_per_minute,omitempty"`
	RemainingRequestsPerMinute *wayfinderKnownUint64 `json:"remaining_requests_per_minute,omitempty"`
	AvailableUnits             *wayfinderKnownUint64 `json:"available_units,omitempty"`
	Unit                       *string               `json:"unit,omitempty"`
}

type wayfinderQueue struct {
	Depth                wayfinderKnownUint64 `json:"depth"`
	ExpectedWaitSeconds  wayfinderKnownUint64 `json:"expected_wait_seconds"`
	AcceptingSubmissions wayfinderKnownBool   `json:"accepting_submissions"`
}

type wayfinderStartup struct {
	State                wayfinderKnownString `json:"state"`
	ExpectedReadySeconds wayfinderKnownUint64 `json:"expected_ready_seconds"`
}

type wayfinderLivePrice struct {
	UnitPriceMicros wayfinderKnownUint64 `json:"unit_price_micros"`
	Currency        string               `json:"currency"`
	PricedAtUnix    int64                `json:"priced_at_unix"`
}

type wayfinderObservation struct {
	RecordID       string               `json:"record_id"`
	TargetID       string               `json:"target_id"`
	Reachable      wayfinderKnownBool   `json:"reachable"`
	Authenticated  wayfinderKnownBool   `json:"authenticated"`
	Circuit        wayfinderKnownString `json:"circuit"`
	Throttle       wayfinderKnownString `json:"throttle"`
	Capacity       wayfinderCapacity    `json:"capacity"`
	Queue          *wayfinderQueue      `json:"queue"`
	Startup        *wayfinderStartup    `json:"startup"`
	LivePrice      *wayfinderLivePrice  `json:"live_price"`
	ObservedAtUnix int64                `json:"observed_at_unix"`
	ExpiresAtUnix  int64                `json:"expires_at_unix"`
	Provenance     string               `json:"provenance"`
}

type wayfinderEvaluateResult struct {
	SchemaVersion            string                          `json:"schema_version"`
	CorrelationID            string                          `json:"correlation_id"`
	DecisionID               string                          `json:"decision_id"`
	Request                  wayfinderEvaluateRequest        `json:"request"`
	Disposition              string                          `json:"disposition"`
	Recommendation           *wayfinderRecommendation        `json:"recommendation"`
	Candidates               []wayfinderCandidateDisposition `json:"candidates"`
	Fingerprints             wayfinderFingerprints           `json:"fingerprints"`
	PolicyVersion            string                          `json:"policy_version"`
	IssuedAtUnix             int64                           `json:"issued_at_unix"`
	ExpiresAtUnix            int64                           `json:"expires_at_unix"`
	AdvisoryOnly             bool                            `json:"advisory_only"`
	NoActiveMigration        bool                            `json:"no_active_migration"`
	AlternativesAdvisoryOnly bool                            `json:"alternatives_advisory_only"`
	Reevaluation             wayfinderReevaluation           `json:"reevaluation"`
}

type wayfinderFingerprints struct {
	Request         string `json:"request"`
	Inventory       string `json:"inventory"`
	ModelAssessment string `json:"model_assessment"`
	AccountScope    string `json:"account_scope"`
	Evidence        string `json:"evidence"`
	Policy          string `json:"policy"`
	Clock           string `json:"clock"`
}

type wayfinderRecommendation struct {
	CandidateID              string                  `json:"candidate_id"`
	Model                    wayfinderSelectedModel  `json:"model"`
	ExecutionTarget          wayfinderSelectedTarget `json:"execution_target"`
	BillingMode              string                  `json:"billing_mode"`
	MatchedPreferredProvider bool                    `json:"matched_preferred_provider"`
	MatchedPreferredModel    bool                    `json:"matched_preferred_model"`
	RankComponents           wayfinderRankComponents `json:"rank_components"`
}

type wayfinderSelectedModel struct {
	CanonicalModel  string `json:"canonical_model"`
	ServeAs         string `json:"serve_as"`
	ReasoningEffort string `json:"reasoning_effort"`
}

type wayfinderSelectedTarget struct {
	TargetID      string `json:"target_id"`
	ConfigDigest  string `json:"config_digest"`
	AdapterID     string `json:"adapter_id"`
	AdapterDigest string `json:"adapter_digest"`
}

type wayfinderGateResult struct {
	Gate   string  `json:"gate"`
	Result string  `json:"result"`
	Detail *string `json:"detail"`
}

type wayfinderEvidenceRef struct {
	RecordID       string `json:"record_id"`
	ObservedAtUnix int64  `json:"observed_at_unix"`
	ExpiresAtUnix  int64  `json:"expires_at_unix"`
}

type wayfinderRankComponents struct {
	QualityScore             uint32   `json:"quality_score"`
	LatencyScore             uint32   `json:"latency_score"`
	CostScore                uint32   `json:"cost_score"`
	WeightedScore            uint32   `json:"weighted_score"`
	CostBasis                string   `json:"cost_basis"`
	MatchedPreferredProvider bool     `json:"matched_preferred_provider"`
	MatchedPreferredModel    bool     `json:"matched_preferred_model"`
	UnknownInputs            []string `json:"unknown_inputs"`
}

type wayfinderCandidateDisposition struct {
	CandidateID     string                   `json:"candidate_id"`
	Disposition     string                   `json:"disposition"`
	Model           wayfinderModelRecord     `json:"model"`
	ExecutionTarget wayfinderExecutionTarget `json:"execution_target"`
	GateResults     []wayfinderGateResult    `json:"gate_results"`
	RankComponents  *wayfinderRankComponents `json:"rank_components"`
	EvidenceRefs    []wayfinderEvidenceRef   `json:"evidence_refs"`
}

type wayfinderReevaluation struct {
	ReevaluateAtUnix   int64  `json:"re_evaluate_at_unix"`
	Reason             string `json:"reason"`
	RequiresNewRequest bool   `json:"requires_new_request"`
	NoPaidProbe        bool   `json:"no_paid_probe"`
	NoActiveMigration  bool   `json:"no_active_migration"`
}

// NewWayfinderRecoveryAdvisor validates a loopback base URL and a complete,
// strictly typed routing/v3 request template before any HTTP request is possible.
func NewWayfinderRecoveryAdvisor(baseURL string, template []byte) (*WayfinderRecoveryAdvisor, error) {
	endpoint, err := recoveryWayfinderEvaluateEndpoint(baseURL)
	if err != nil {
		return nil, err
	}
	packet, err := decodeWayfinderEvaluateTemplate(template)
	if err != nil {
		return nil, fmt.Errorf("decode wayfinder routing/v3 request template: %w", err)
	}
	client := &http.Client{
		Transport: http.DefaultTransport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return fmt.Errorf("wayfinder redirects are not allowed")
		},
	}
	return &WayfinderRecoveryAdvisor{endpoint: endpoint, template: packet, client: client, now: time.Now}, nil
}

// Recommend submits the canonical operator packet after narrowing it to
// configured, not-yet-attempted recovery targets. Callers own lifecycle actions.
func (a *WayfinderRecoveryAdvisor) Recommend(ctx context.Context, request RecoveryRequest) (string, error) {
	if a == nil || a.endpoint == "" || a.client == nil || a.now == nil {
		return "", fmt.Errorf("wayfinder endpoint is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	correlationID := strings.TrimSpace(request.CorrelationID)
	if !wayfinderSafeString(correlationID) || request.Now.IsZero() {
		return "", fmt.Errorf("wayfinder recovery request requires a safe correlation id and current time")
	}
	if len(request.Targets) > maximumWayfinderRecords {
		return "", fmt.Errorf("wayfinder recovery request exceeds %d targets", maximumWayfinderRecords)
	}
	remaining := uniqueRecoveryTargets(request.Targets)
	if len(remaining) == 0 || !wayfinderUniqueSafe(remaining) {
		return "", fmt.Errorf("wayfinder recovery request has no safe remaining targets")
	}
	if len(remaining) > maximumWayfinderRecords {
		return "", fmt.Errorf("wayfinder recovery request exceeds %d remaining targets", maximumWayfinderRecords)
	}
	remainingSet := make(map[string]struct{}, len(remaining))
	for _, target := range remaining {
		remainingSet[target] = struct{}{}
	}

	packet, err := cloneWayfinderRequest(a.template)
	if err != nil {
		return "", fmt.Errorf("clone wayfinder routing/v3 request: %w", err)
	}
	packet.CorrelationID = correlationID
	packet.NowUnix = request.Now.Unix()
	packet.Candidates = packet.Candidates[:0]
	for _, candidate := range a.template.Candidates {
		if _, ok := remainingSet[candidate.ExecutionTarget.TargetID]; ok {
			packet.Candidates = append(packet.Candidates, candidate)
		}
	}
	if len(packet.Candidates) == 0 {
		return "", fmt.Errorf("wayfinder request template has no candidate for remaining recovery targets")
	}
	narrowWayfinderRequest(&packet, remaining)
	if err := validateWayfinderRequest(&packet); err != nil {
		return "", fmt.Errorf("build wayfinder routing/v3 request: %w", err)
	}
	canonicalizeWayfinderRequest(&packet)
	body, err := json.Marshal(packet)
	if err != nil {
		return "", fmt.Errorf("encode wayfinder routing/v3 request: %w", err)
	}
	if len(body) > maximumWayfinderPacketBytes {
		return "", fmt.Errorf("wayfinder routing/v3 request exceeds %d bytes", maximumWayfinderPacketBytes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build wayfinder routing/v3 request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Wayfinder-Request", wayfinderRequestHeaderValue)
	response, err := a.client.Do(req)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		}
		return "", fmt.Errorf("call wayfinder routing/v3 evaluate: %w", err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		_ = response.Body.Close()
		return "", contextErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		closeErr := response.Body.Close()
		if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		}
		if readErr != nil || closeErr != nil {
			return "", wayfinderResponseIOError("read error response", readErr, closeErr)
		}
		return "", fmt.Errorf("wayfinder routing/v3 evaluate returned %s", response.Status)
	}
	responseBody, readErr := readBoundedWayfinderPacket(response.Body)
	closeErr := response.Body.Close()
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}
	if readErr != nil || closeErr != nil {
		return "", wayfinderResponseIOError("read response", readErr, closeErr)
	}
	var result wayfinderEvaluateResult
	if err := decodeSingleJSON(responseBody, &result); err != nil {
		return "", fmt.Errorf("decode wayfinder routing/v3 response: %w", err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}
	validationErr := validateWayfinderResult(result, packet, remainingSet)
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}
	if validationErr != nil {
		return "", validationErr
	}
	postResponseNow := a.now().Unix()
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}
	if result.ExpiresAtUnix <= postResponseNow || result.Reevaluation.ReevaluateAtUnix <= postResponseNow {
		return "", fmt.Errorf("wayfinder response fresh advisory validity window expired")
	}
	return result.Recommendation.ExecutionTarget.TargetID, nil
}

func narrowWayfinderRequest(request *wayfinderEvaluateRequest, remainingTargets []string) {
	survivingTargets := make(map[string]struct{}, len(request.Candidates))
	assessmentRefs := make(map[string]struct{}, len(request.Candidates))
	entitlementRefs := make(map[string]struct{}, len(request.Candidates))
	observationRefs := make(map[string]struct{}, len(request.Candidates))
	for _, candidate := range request.Candidates {
		survivingTargets[candidate.ExecutionTarget.TargetID] = struct{}{}
		if candidate.ModelAssessmentRef != nil {
			assessmentRefs[*candidate.ModelAssessmentRef] = struct{}{}
		}
		entitlementRefs[candidate.EntitlementRef] = struct{}{}
		observationRefs[candidate.ObservationRef] = struct{}{}
	}
	request.Constraints.Work.AllowedTargetIDs = filterWayfinderStrings(remainingTargets, survivingTargets)
	request.ModelAssessments = filterWayfinderRecords(request.ModelAssessments, assessmentRefs, func(record wayfinderModelAssessment) string { return record.RecordID })
	request.Entitlements = filterWayfinderRecords(request.Entitlements, entitlementRefs, func(record wayfinderEntitlement) string { return record.RecordID })
	request.Observations = filterWayfinderRecords(request.Observations, observationRefs, func(record wayfinderObservation) string { return record.RecordID })
}

func filterWayfinderStrings(values []string, allowed map[string]struct{}) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := allowed[value]; ok {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func filterWayfinderRecords[T any](records []T, refs map[string]struct{}, recordID func(T) string) []T {
	filtered := make([]T, 0, len(records))
	for _, record := range records {
		if _, ok := refs[recordID(record)]; ok {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

func wayfinderResponseIOError(readOperation string, readErr, closeErr error) error {
	var responseErrors []error
	if readErr != nil {
		responseErrors = append(responseErrors, fmt.Errorf("%s from wayfinder routing/v3: %w", readOperation, readErr))
	}
	if closeErr != nil {
		responseErrors = append(responseErrors, fmt.Errorf("close wayfinder routing/v3 response: %w", closeErr))
	}
	return errors.Join(responseErrors...)
}

func decodeWayfinderEvaluateTemplate(data []byte) (wayfinderEvaluateRequest, error) {
	var packet wayfinderEvaluateRequest
	if len(data) == 0 || len(data) > maximumWayfinderPacketBytes {
		return packet, fmt.Errorf("template must contain at most %d bytes", maximumWayfinderPacketBytes)
	}
	if err := decodeSingleJSON(data, &packet); err != nil {
		return packet, err
	}
	if err := validateWayfinderRequest(&packet); err != nil {
		return packet, err
	}
	return packet, nil
}

// decodeSingleJSON rejects duplicate keys, case variants, omitted required
// fields, unknown fields, non-integer numbers, and trailing JSON values.
func decodeSingleJSON(data []byte, result any) error {
	input, err := decodeStrictJSONValue(data)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return err
	}
	decoded, err := decodeStrictJSONValue(canonical)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(input, decoded) {
		return fmt.Errorf("JSON keys and required fields must match the routing/v3 DTO exactly")
	}
	return nil
}

func decodeStrictJSONValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readStrictJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values after %v", token)
		}
		return nil, err
	}
	return value, nil
}

func readStrictJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		seenFolded := make(map[string]string)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok || !wayfinderCanonicalJSONKey(key) {
				return nil, fmt.Errorf("non-canonical JSON object key %q", key)
			}
			folded := strings.ToLower(key)
			if previous, exists := seenFolded[folded]; exists {
				return nil, fmt.Errorf("duplicate JSON object key %q conflicts with %q", key, previous)
			}
			seenFolded[folded] = key
			value, err := readStrictJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("unterminated JSON object")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := readStrictJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("unterminated JSON array")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func wayfinderCanonicalJSONKey(key string) bool {
	if key == "" || key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for _, character := range []byte(key) {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validateWayfinderRequest(request *wayfinderEvaluateRequest) error {
	if request.SchemaVersion != "routing/v3" || request.NowUnix < 1 || !wayfinderSafeString(request.CorrelationID) || !wayfinderSafeString(request.PolicyVersion) {
		return fmt.Errorf("invalid routing/v3 request envelope")
	}
	workload := request.Workload
	// The public evaluate endpoint rejects bootstrap profiles; accepting one
	// here would make a structurally valid local template fail every advisory
	// call and silently degrade to fallback routing.
	if !wayfinderOneOf(workload.ProfileSource, "caller", "inferred") ||
		!wayfinderOneOf(workload.Operation, "coordinate", "plan", "generate", "transform", "review", "reconcile", "execute") ||
		!wayfinderOneOf(workload.Artifact, "code", "tests", "plan", "merge", "text", "structured_data", "other") ||
		!wayfinderOneOf(workload.Complexity, "low", "medium", "high") || !wayfinderOneOf(workload.Consequence, "low", "medium", "high") ||
		!wayfinderOneOf(workload.ExecutionShape, "one_shot", "iterative", "long_running", "singleton") ||
		!wayfinderOneOf(workload.ReasoningRequirement, "low", "medium", "high") ||
		workload.MinimumContextTokens < 1 || workload.MinimumContextTokens > 2097152 || workload.ExpectedInputTokens > 2097152 || workload.ExpectedOutputTokens > 2097152 ||
		len(workload.RequiredTools) > maximumWayfinderTools || !wayfinderUniqueSafe(workload.RequiredTools) ||
		!wayfinderOneOf(workload.StructuredOutput, "none", "json", "json_schema") ||
		!wayfinderCanonicalModalities(workload.RequiredInputModalities) || !wayfinderCanonicalModalities(workload.RequiredOutputModalities) {
		return fmt.Errorf("invalid routing/v3 workload")
	}
	policy := request.Constraints.Policy
	if !wayfinderOneOf(policy.DataHandling.RemoteDisclosure, "forbidden", "private_network_only", "permitted") ||
		!wayfinderOptionalNonemptyUniqueSafe(policy.Residency.AllowedRegions) || !wayfinderOptionalNonemptyUniqueProviderIDs(policy.Providers.AllowedProviderIDs) ||
		!wayfinderUniqueProviderIDs(policy.Providers.DeniedProviderIDs) || (policy.Spend.Currency != nil && !wayfinderOneOf(*policy.Spend.Currency, "USD", "EUR", "GBP", "JPY", "CHF", "CAD", "AUD")) {
		return fmt.Errorf("invalid routing/v3 policy constraints")
	}
	work := request.Constraints.Work
	if len(work.AllowedTargetIDs) == 0 || len(work.AllowedTargetIDs) > maximumWayfinderRecords || !wayfinderUniqueSafe(work.AllowedTargetIDs) || !wayfinderOptionalSafe(work.ModelID) || !wayfinderOptionalSafe(work.TargetID) {
		return fmt.Errorf("invalid routing/v3 work constraints")
	}
	if request.Objectives.Quality > 100 || request.Objectives.Latency > 100 || request.Objectives.Cost > 100 || request.Objectives.Quality+request.Objectives.Latency+request.Objectives.Cost != 100 {
		return fmt.Errorf("routing/v3 objectives must be weights from 0 through 100 summing to 100")
	}
	if !wayfinderOptionalProviderID(request.Preferences.PreferredProviderID) || !wayfinderOptionalSafe(request.Preferences.PreferredModelID) {
		return fmt.Errorf("invalid routing/v3 preferences")
	}
	if len(request.Candidates) == 0 || len(request.Candidates) > maximumWayfinderRecords || len(request.ModelAssessments) > maximumWayfinderRecords || len(request.Entitlements) > maximumWayfinderRecords || len(request.Observations) > maximumWayfinderRecords {
		return fmt.Errorf("invalid routing/v3 record array length")
	}
	candidateIDs := make(map[string]struct{}, len(request.Candidates))
	allowed := wayfinderStringSet(work.AllowedTargetIDs)
	for i := range request.Candidates {
		candidate := &request.Candidates[i]
		if !wayfinderUniqueInsert(candidateIDs, candidate.CandidateID) || validateWayfinderModel(candidate.Model) != nil || validateWayfinderTarget(candidate.ExecutionTarget) != nil || validateWayfinderEconomics(candidate.Economics) != nil ||
			!wayfinderOptionalSafe(candidate.ModelAssessmentRef) || !wayfinderSafeString(candidate.EntitlementRef) || !wayfinderSafeString(candidate.ObservationRef) {
			return fmt.Errorf("invalid routing/v3 candidate %q", candidate.CandidateID)
		}
		if _, ok := allowed[candidate.ExecutionTarget.TargetID]; !ok || (work.TargetID != nil && *work.TargetID != candidate.ExecutionTarget.TargetID) || (work.ModelID != nil && *work.ModelID != candidate.Model.CanonicalModel) {
			return fmt.Errorf("candidate %q contradicts work constraints", candidate.CandidateID)
		}
	}
	assessmentIDs := make(map[string]struct{}, len(request.ModelAssessments))
	assessments := make(map[string]wayfinderModelAssessment, len(request.ModelAssessments))
	for _, assessment := range request.ModelAssessments {
		if !wayfinderUniqueInsert(assessmentIDs, assessment.RecordID) || !wayfinderSafeString(assessment.CanonicalModel) || !wayfinderSafeString(assessment.VariantRef) || !wayfinderSafeString(assessment.Provenance) ||
			assessment.AssessedAtUnix < 1 || assessment.ExpiresAtUnix <= request.NowUnix || assessment.AssessedAtUnix > request.NowUnix || len(assessment.OperationFitness) > 7 {
			return fmt.Errorf("invalid routing/v3 model assessment")
		}
		operations := make(map[string]struct{}, len(assessment.OperationFitness))
		for _, fitness := range assessment.OperationFitness {
			if !wayfinderOneOf(fitness.Operation, "coordinate", "plan", "generate", "transform", "review", "reconcile", "execute") || fitness.Fitness > 100 || !wayfinderUniqueInsert(operations, fitness.Operation) {
				return fmt.Errorf("invalid routing/v3 operation fitness")
			}
		}
		assessments[assessment.RecordID] = assessment
	}
	entitlementIDs := make(map[string]struct{}, len(request.Entitlements))
	entitlements := make(map[string]wayfinderEntitlement, len(request.Entitlements))
	for _, entitlement := range request.Entitlements {
		allocation := entitlement.BillingMode == "allocation"
		if !wayfinderUniqueInsert(entitlementIDs, entitlement.RecordID) || !wayfinderAccountRef(entitlement.AccountRef) || !wayfinderSafeString(entitlement.Provenance) ||
			!entitlement.Enabled || entitlement.AuthorizationState != "authorized" ||
			!wayfinderOneOf(entitlement.BillingMode, "subscription", "prepaid", "pay_as_you_go", "allocation", "unknown") ||
			(entitlement.AllocationBalance != nil) != allocation || entitlement.ObservedAtUnix < 1 || entitlement.ExpiresAtUnix <= request.NowUnix || entitlement.ObservedAtUnix > request.NowUnix {
			return fmt.Errorf("invalid routing/v3 entitlement")
		}
		if entitlement.AllocationBalance != nil && (!wayfinderOneOf(entitlement.AllocationBalance.Unit, "service_units", "node_hours", "gpu_hours", "tokens") || !wayfinderValidKnownUint64(entitlement.AllocationBalance.Remaining, false)) {
			return fmt.Errorf("invalid routing/v3 allocation balance")
		}
		entitlements[entitlement.RecordID] = entitlement
	}
	observationIDs := make(map[string]struct{}, len(request.Observations))
	observations := make(map[string]wayfinderObservation, len(request.Observations))
	for _, observation := range request.Observations {
		if !wayfinderUniqueInsert(observationIDs, observation.RecordID) || !wayfinderSafeString(observation.TargetID) || !wayfinderSafeString(observation.Provenance) ||
			!wayfinderValidKnownBool(observation.Reachable) || !wayfinderValidKnownBool(observation.Authenticated) ||
			!wayfinderValidKnownString(observation.Circuit, "closed", "open", "cooling") || !wayfinderValidKnownString(observation.Throttle, "none", "gateway", "upstream", "both") ||
			validateWayfinderCapacity(observation.Capacity) != nil || observation.ObservedAtUnix < 1 || observation.ExpiresAtUnix <= request.NowUnix || observation.ObservedAtUnix > request.NowUnix {
			return fmt.Errorf("invalid routing/v3 observation")
		}
		if observation.Queue != nil && (!wayfinderValidKnownUint64(observation.Queue.Depth, false) || !wayfinderValidKnownUint64(observation.Queue.ExpectedWaitSeconds, false) || !wayfinderValidKnownBool(observation.Queue.AcceptingSubmissions)) {
			return fmt.Errorf("invalid routing/v3 queue observation")
		}
		if observation.Startup != nil && (!wayfinderValidKnownString(observation.Startup.State, "warm", "cold", "starting") || !wayfinderValidKnownUint64(observation.Startup.ExpectedReadySeconds, false)) {
			return fmt.Errorf("invalid routing/v3 startup observation")
		}
		if observation.LivePrice != nil && (!wayfinderValidKnownUint64(observation.LivePrice.UnitPriceMicros, false) || !wayfinderOneOf(observation.LivePrice.Currency, "USD", "EUR", "GBP", "JPY", "CHF", "CAD", "AUD") || observation.LivePrice.PricedAtUnix < 1 || observation.LivePrice.PricedAtUnix > observation.ObservedAtUnix) {
			return fmt.Errorf("invalid routing/v3 live price")
		}
		observations[observation.RecordID] = observation
	}
	for _, candidate := range request.Candidates {
		if candidate.ModelAssessmentRef != nil {
			assessment, ok := assessments[*candidate.ModelAssessmentRef]
			if !ok || assessment.CanonicalModel != candidate.Model.CanonicalModel || assessment.VariantRef != candidate.Model.VariantRef {
				return fmt.Errorf("routing/v3 candidate model assessment binding is invalid")
			}
		}
		entitlement, ok := entitlements[candidate.EntitlementRef]
		if !ok || entitlement.AccountRef != candidate.ExecutionTarget.AccountRef {
			return fmt.Errorf("routing/v3 candidate entitlement binding is invalid")
		}
		observation, ok := observations[candidate.ObservationRef]
		if !ok {
			return fmt.Errorf("routing/v3 candidate observation binding is invalid")
		}
		if observation.TargetID != candidate.ExecutionTarget.TargetID || observation.Capacity.Model != candidate.ExecutionTarget.CapacityModel ||
			(observation.Queue != nil) != (candidate.ExecutionTarget.AdmissionModel == "queued") || (observation.Startup != nil) != (candidate.ExecutionTarget.ActivationModel == "on_demand") ||
			(observation.LivePrice != nil) != (candidate.Economics.Volatility == "variable") || (observation.LivePrice != nil && candidate.Economics.Currency != nil && observation.LivePrice.Currency != *candidate.Economics.Currency) {
			return fmt.Errorf("routing/v3 candidate evidence binding is invalid")
		}
	}
	return nil
}

func validateWayfinderModel(model wayfinderModelRecord) error {
	if !wayfinderSafeString(model.CanonicalModel) || !wayfinderSafeString(model.ServeAs) || !wayfinderSafeString(model.VariantRef) ||
		!wayfinderOneOf(model.ReasoningEffort, "none", "low", "medium", "high") || !wayfinderCanonicalModalities(model.InputModalities) || !wayfinderCanonicalModalities(model.OutputModalities) || model.MaxContextTokens < 1 || model.MaxOutputTokens < 1 {
		return errors.New("invalid model")
	}
	return nil
}

func validateWayfinderTarget(target wayfinderExecutionTarget) error {
	if !wayfinderSafeString(target.TargetID) || !wayfinderOneOf(target.FabricKind, "local_resident", "onprem_endpoint", "onprem_scheduler", "cloud_endpoint", "cloud_scheduler") ||
		!wayfinderProviderID(target.ProviderID) || !wayfinderAccountRef(target.AccountRef) || !wayfinderOneOf(target.AdmissionModel, "immediate", "queued") ||
		!wayfinderOneOf(target.ActivationModel, "always_on", "on_demand") || !wayfinderOneOf(target.CapacityModel, "concurrency_slots", "token_rate", "allocation_units", "unmetered") ||
		target.DeploymentMaxContextTokens < 1 || target.DeploymentMaxOutputTokens < 1 || !wayfinderSafeString(target.Residency) ||
		!wayfinderOneOf(target.DataHandling, "local_only", "private_network", "remote_provider") || !wayfinderOneOf(target.Isolation, "dedicated", "shared_tenant") ||
		!validWayfinderDigest(target.ConfigDigest) || !wayfinderAdapterID(target.AdapterID) || !validWayfinderDigest(target.AdapterDigest) {
		return errors.New("invalid target")
	}
	if (target.ResourceProfile != nil) != (target.AdmissionModel == "queued") {
		return errors.New("resource profile does not match admission model")
	}
	if profile := target.ResourceProfile; profile != nil {
		if !wayfinderOneOf(profile.Scheduler, "slurm", "pbs", "kubernetes", "managed_batch") || !wayfinderSafeString(profile.QueueRef) || !wayfinderOptionalSafe(profile.QoSRef) || profile.NodesPerAllocation < 1 || !wayfinderOptionalSafe(profile.GPUModel) || profile.CPUCoresPerNode < 1 || profile.MemoryGiBPerNode < 1 || !wayfinderOneOf(profile.Interconnect, "ethernet", "infiniband", "nvlink", "unknown_topology") || profile.MaxWalltimeSeconds < 1 {
			return errors.New("invalid resource profile")
		}
	}
	binding := target.InvocationBinding
	if binding.BindingKind != "adapter_handle" || !wayfinderSafeString(binding.BindingRef) || wayfinderLooksLikeEndpoint(binding.BindingRef) ||
		!wayfinderAdapterID(binding.AdapterID) || binding.AdapterID != target.AdapterID || binding.AdapterDigest != target.AdapterDigest {
		return errors.New("invalid invocation binding")
	}
	return nil
}

func validateWayfinderEconomics(economics wayfinderEconomics) error {
	if !wayfinderOneOf(economics.PricingModel, "per_token", "per_second", "per_allocation_unit", "included_in_subscription", "unpriced") ||
		(economics.Currency != nil && !wayfinderOneOf(*economics.Currency, "USD", "EUR", "GBP", "JPY", "CHF", "CAD", "AUD")) ||
		(economics.AllocationUnit != nil && !wayfinderOneOf(*economics.AllocationUnit, "service_units", "node_hours", "gpu_hours", "tokens")) ||
		!wayfinderOneOf(economics.PriceBasis, "posted", "contracted", "estimated") || !wayfinderOneOf(economics.Volatility, "fixed", "variable") {
		return errors.New("invalid economics")
	}
	perToken := economics.PricingModel == "per_token"
	perUnit := economics.PricingModel == "per_second" || economics.PricingModel == "per_allocation_unit"
	priced := perToken || perUnit
	if (economics.InputPriceMicros != nil) != perToken || (economics.OutputPriceMicros != nil) != perToken || (economics.UnitPriceMicros != nil) != perUnit || (!priced && economics.Currency != nil) || (economics.AllocationUnit != nil) != (economics.PricingModel == "per_allocation_unit") {
		return errors.New("economics fields do not match pricing model")
	}
	return nil
}

func validateWayfinderCapacity(capacity wayfinderCapacity) error {
	if !wayfinderOneOf(capacity.Model, "concurrency_slots", "token_rate", "allocation_units", "unmetered") {
		return errors.New("invalid capacity model")
	}
	valid := false
	switch capacity.Model {
	case "concurrency_slots":
		valid = capacity.FreeSlots != nil && capacity.TotalSlots != nil && capacity.RemainingTokensPerMinute == nil && capacity.RemainingRequestsPerMinute == nil && capacity.AvailableUnits == nil && capacity.Unit == nil && wayfinderValidKnownUint64(*capacity.FreeSlots, false) && wayfinderValidKnownUint64(*capacity.TotalSlots, true)
		if valid && capacity.FreeSlots.Known && capacity.TotalSlots.Known && *capacity.FreeSlots.Value > *capacity.TotalSlots.Value {
			valid = false
		}
	case "token_rate":
		valid = capacity.FreeSlots == nil && capacity.TotalSlots == nil && capacity.RemainingTokensPerMinute != nil && capacity.RemainingRequestsPerMinute != nil && capacity.AvailableUnits == nil && capacity.Unit == nil && wayfinderValidKnownUint64(*capacity.RemainingTokensPerMinute, false) && wayfinderValidKnownUint64(*capacity.RemainingRequestsPerMinute, false)
	case "allocation_units":
		valid = capacity.FreeSlots == nil && capacity.TotalSlots == nil && capacity.RemainingTokensPerMinute == nil && capacity.RemainingRequestsPerMinute == nil && capacity.AvailableUnits != nil && capacity.Unit != nil && wayfinderValidKnownUint64(*capacity.AvailableUnits, false) && wayfinderOneOf(*capacity.Unit, "service_units", "node_hours", "gpu_hours", "tokens")
	case "unmetered":
		valid = capacity.FreeSlots == nil && capacity.TotalSlots == nil && capacity.RemainingTokensPerMinute == nil && capacity.RemainingRequestsPerMinute == nil && capacity.AvailableUnits == nil && capacity.Unit == nil
	}
	if !valid {
		return errors.New("capacity fields do not match model")
	}
	return nil
}

func validateWayfinderResult(result wayfinderEvaluateResult, request wayfinderEvaluateRequest, remaining map[string]struct{}) error {
	if result.SchemaVersion != "routing/v3" || result.CorrelationID != request.CorrelationID || !reflect.DeepEqual(result.Request, request) {
		return fmt.Errorf("wayfinder response does not echo the exact canonical request")
	}
	expectedFingerprints := wayfinderRequestFingerprints(request)
	if !reflect.DeepEqual(result.Fingerprints, expectedFingerprints) || result.DecisionID != wayfinderDecisionID(expectedFingerprints) || result.PolicyVersion != request.PolicyVersion || result.IssuedAtUnix != request.NowUnix {
		return fmt.Errorf("wayfinder response is not bound to the submitted request, policy, and fingerprints")
	}
	if result.ExpiresAtUnix < result.IssuedAtUnix || !result.AdvisoryOnly || !result.NoActiveMigration || !result.AlternativesAdvisoryOnly || !result.Reevaluation.RequiresNewRequest || !result.Reevaluation.NoPaidProbe || !result.Reevaluation.NoActiveMigration || result.Reevaluation.ReevaluateAtUnix < result.IssuedAtUnix {
		return fmt.Errorf("wayfinder response identity or fresh advisory validity window is invalid")
	}
	if result.Disposition != "selected" || result.Recommendation == nil || result.ExpiresAtUnix <= result.IssuedAtUnix || result.Reevaluation.Reason != "evidence_expiry" {
		return fmt.Errorf("wayfinder response has no selected recommendation")
	}
	if len(result.Candidates) != len(request.Candidates) {
		return fmt.Errorf("wayfinder response does not bind every submitted candidate")
	}
	selected := -1
	evidenceExpiry := int64(0)
	for i, disposition := range result.Candidates {
		candidate := request.Candidates[i]
		if disposition.CandidateID != candidate.CandidateID || !reflect.DeepEqual(disposition.Model, candidate.Model) || !reflect.DeepEqual(disposition.ExecutionTarget, candidate.ExecutionTarget) || !wayfinderOneOf(disposition.Disposition, "selected", "eligible_not_selected", "rejected") || len(disposition.GateResults) == 0 {
			return fmt.Errorf("wayfinder candidate disposition is not bound to submitted candidate %q", candidate.CandidateID)
		}
		expectedEvidence := wayfinderExpectedEvidenceRefs(request, candidate)
		if err := validateWayfinderDisposition(disposition, expectedEvidence); err != nil {
			return fmt.Errorf("invalid wayfinder candidate disposition: %w", err)
		}
		for _, evidence := range expectedEvidence {
			if evidenceExpiry == 0 || evidence.ExpiresAtUnix < evidenceExpiry {
				evidenceExpiry = evidence.ExpiresAtUnix
			}
		}
		if disposition.Disposition == "selected" {
			if selected >= 0 {
				return fmt.Errorf("wayfinder response selects more than one candidate")
			}
			selected = i
		}
	}
	if selected < 0 {
		return fmt.Errorf("wayfinder response has no selected candidate disposition")
	}
	if evidenceExpiry == 0 || result.ExpiresAtUnix > evidenceExpiry || result.Reevaluation.ReevaluateAtUnix > result.ExpiresAtUnix {
		return fmt.Errorf("wayfinder response validity is not bounded by submitted evidence")
	}
	candidate := request.Candidates[selected]
	disposition := result.Candidates[selected]
	recommendation := result.Recommendation
	if err := validateWayfinderSelectedEligibility(request, candidate, disposition); err != nil {
		return fmt.Errorf("wayfinder selected candidate is not eligible: %w", err)
	}
	wantModel := wayfinderSelectedModel{CanonicalModel: candidate.Model.CanonicalModel, ServeAs: candidate.Model.ServeAs, ReasoningEffort: candidate.Model.ReasoningEffort}
	wantTarget := wayfinderSelectedTarget{TargetID: candidate.ExecutionTarget.TargetID, ConfigDigest: candidate.ExecutionTarget.ConfigDigest, AdapterID: candidate.ExecutionTarget.AdapterID, AdapterDigest: candidate.ExecutionTarget.AdapterDigest}
	if recommendation.CandidateID != candidate.CandidateID || !reflect.DeepEqual(recommendation.Model, wantModel) || !reflect.DeepEqual(recommendation.ExecutionTarget, wantTarget) || disposition.RankComponents == nil || !reflect.DeepEqual(recommendation.RankComponents, *disposition.RankComponents) {
		return fmt.Errorf("wayfinder recommendation does not match the selected submitted candidate")
	}
	matchedProvider := request.Preferences.PreferredProviderID != nil && *request.Preferences.PreferredProviderID == candidate.ExecutionTarget.ProviderID
	matchedModel := request.Preferences.PreferredModelID != nil && *request.Preferences.PreferredModelID == candidate.Model.CanonicalModel
	if recommendation.MatchedPreferredProvider != matchedProvider || recommendation.MatchedPreferredModel != matchedModel ||
		recommendation.RankComponents.MatchedPreferredProvider != matchedProvider || recommendation.RankComponents.MatchedPreferredModel != matchedModel {
		return fmt.Errorf("wayfinder recommendation preference matches are not bound to the submitted request")
	}
	if _, ok := remaining[recommendation.ExecutionTarget.TargetID]; !ok {
		return fmt.Errorf("wayfinder selected target %q outside remaining recovery targets", recommendation.ExecutionTarget.TargetID)
	}
	entitlement, ok := wayfinderEntitlementFor(request.Entitlements, candidate.EntitlementRef)
	if !ok || recommendation.BillingMode != entitlement.BillingMode || !wayfinderOneOf(recommendation.BillingMode, "subscription", "prepaid", "pay_as_you_go", "allocation") {
		return fmt.Errorf("wayfinder recommendation billing mode is not bound to candidate entitlement")
	}
	return nil
}

func validateWayfinderSelectedEligibility(request wayfinderEvaluateRequest, candidate wayfinderCandidate, disposition wayfinderCandidateDisposition) error {
	expected, err := wayfinderExpectedGateResults(request, candidate)
	if err != nil {
		return err
	}
	for _, gate := range expected {
		if gate.Result != "pass" {
			return fmt.Errorf("bound evidence or policy fails gate %q", gate.Gate)
		}
	}
	if !reflect.DeepEqual(disposition.GateResults, expected) {
		return errors.New("gate results do not match bound evidence and policy")
	}
	return nil
}

func wayfinderExpectedGateResults(request wayfinderEvaluateRequest, candidate wayfinderCandidate) ([]wayfinderGateResult, error) {
	workload := request.Workload
	policy := request.Constraints.Policy
	target := candidate.ExecutionTarget
	model := candidate.Model
	entitlement, ok := wayfinderEntitlementFor(request.Entitlements, candidate.EntitlementRef)
	if !ok {
		return nil, errors.New("selected candidate has no bound entitlement")
	}
	observation, ok := wayfinderObservationFor(request.Observations, candidate.ObservationRef)
	if !ok {
		return nil, errors.New("selected candidate has no bound observation")
	}

	gates := make([]wayfinderGateResult, 0, len(wayfinderGateNames))
	add := func(gate, result, detail string) {
		gates = append(gates, wayfinderGateResultValue(gate, result, detail))
	}

	switch {
	case entitlement.ExpiresAtUnix <= request.NowUnix || entitlement.ObservedAtUnix > request.NowUnix:
		add("entitlement", "unknown_fail_closed", "entitlement_unavailable")
	case !entitlement.Enabled:
		add("entitlement", "fail", "entitlement_disabled")
	case entitlement.AuthorizationState == "suspended":
		add("entitlement", "fail", "authorization_suspended")
	case entitlement.AuthorizationState == "expired":
		add("entitlement", "fail", "authorization_expired")
	case entitlement.AuthorizationState == "unknown":
		add("entitlement", "unknown_fail_closed", "authorization_unknown")
	case entitlement.BillingMode == "unknown":
		add("entitlement", "unknown_fail_closed", "billing_mode_unknown")
	default:
		add("entitlement", "pass", "")
	}
	if entitlement.BillingMode == "allocation" {
		switch {
		case entitlement.AllocationBalance == nil:
			add("allocation", "unknown_fail_closed", "allocation_balance_missing")
		case !entitlement.AllocationBalance.Remaining.Known:
			add("allocation", "unknown_fail_closed", "allocation_balance_unknown")
		case *entitlement.AllocationBalance.Remaining.Value == 0:
			add("allocation", "fail", "allocation_exhausted")
		default:
			add("allocation", "pass", "")
		}
	}

	if policy.Residency.AllowedRegions != nil {
		if wayfinderContains(*policy.Residency.AllowedRegions, target.Residency) {
			add("residency", "pass", "")
		} else {
			add("residency", "fail", "residency_not_allowed")
		}
	}
	permittedHandling := map[string]int{"forbidden": 0, "private_network_only": 1, "permitted": 2}[policy.DataHandling.RemoteDisclosure]
	targetHandling := map[string]int{"local_only": 0, "private_network": 1, "remote_provider": 2}[target.DataHandling]
	switch {
	case targetHandling <= permittedHandling:
		add("data_handling", "pass", "")
	case policy.DataHandling.RemoteDisclosure == "forbidden":
		add("data_handling", "fail", "local_only_required")
	default:
		add("data_handling", "fail", "private_network_required")
	}
	if policy.Providers.AllowedProviderIDs != nil || len(policy.Providers.DeniedProviderIDs) > 0 {
		allowed := policy.Providers.AllowedProviderIDs == nil || wayfinderContains(*policy.Providers.AllowedProviderIDs, target.ProviderID)
		denied := wayfinderContains(policy.Providers.DeniedProviderIDs, target.ProviderID)
		if allowed && !denied {
			add("provider_allowed", "pass", "")
		} else {
			add("provider_allowed", "fail", "provider_not_allowed")
		}
	}
	if wayfinderContainsAll(model.InputModalities, workload.RequiredInputModalities) {
		add("input_modality", "pass", "")
	} else {
		add("input_modality", "fail", "input_modality_unsupported")
	}
	if wayfinderContainsAll(model.OutputModalities, workload.RequiredOutputModalities) {
		add("output_modality", "pass", "")
	} else {
		add("output_modality", "fail", "output_modality_unsupported")
	}
	effectiveContext := model.MaxContextTokens
	if target.DeploymentMaxContextTokens < effectiveContext {
		effectiveContext = target.DeploymentMaxContextTokens
	}
	effectiveOutput := model.MaxOutputTokens
	if target.DeploymentMaxOutputTokens < effectiveOutput {
		effectiveOutput = target.DeploymentMaxOutputTokens
	}
	switch {
	case effectiveContext < workload.MinimumContextTokens:
		add("context", "fail", "context_below_minimum")
	case effectiveOutput < workload.ExpectedOutputTokens:
		add("context", "fail", "output_below_expected")
	default:
		add("context", "pass", "")
	}
	if len(workload.RequiredTools) > 0 {
		if model.SupportsTools {
			add("tools", "pass", "")
		} else {
			add("tools", "fail", "tools_unsupported")
		}
	}
	if workload.StructuredOutput != "none" {
		if model.SupportsStructuredOutput {
			add("structured_output", "pass", "")
		} else {
			add("structured_output", "fail", "structured_output_unsupported")
		}
	}
	if target.AdmissionModel == "queued" {
		if target.ResourceProfile != nil {
			add("resource_profile", "pass", "")
		} else {
			add("resource_profile", "unknown_fail_closed", "resource_profile_missing")
		}
	}

	addKnownBoolGate := func(gate string, value wayfinderKnownBool, falseDetail, unknownDetail string) {
		switch {
		case !value.Known:
			add(gate, "unknown_fail_closed", unknownDetail)
		case !*value.Value:
			add(gate, "fail", falseDetail)
		default:
			add(gate, "pass", "")
		}
	}
	addKnownBoolGate("reachable", observation.Reachable, "not_reachable", "reachable_unknown")
	addKnownBoolGate("authenticated", observation.Authenticated, "not_authenticated", "authenticated_unknown")
	if !observation.Circuit.Known {
		add("circuit", "unknown_fail_closed", "circuit_unknown")
	} else {
		switch *observation.Circuit.Value {
		case "closed":
			add("circuit", "pass", "")
		case "open":
			add("circuit", "fail", "circuit_open")
		case "cooling":
			add("circuit", "fail", "circuit_cooling")
		}
	}
	if !observation.Throttle.Known {
		add("throttle", "unknown_fail_closed", "throttle_unknown")
	} else {
		switch *observation.Throttle.Value {
		case "none":
			add("throttle", "pass", "")
		case "gateway":
			add("throttle", "fail", "throttled_gateway")
		case "upstream":
			add("throttle", "fail", "throttled_upstream")
		case "both":
			add("throttle", "fail", "throttled_gateway_and_upstream")
		}
	}
	gates = append(gates, wayfinderCapacityGate(target, observation))
	if target.AdmissionModel == "queued" {
		switch {
		case observation.Queue == nil || !observation.Queue.AcceptingSubmissions.Known:
			add("queue_open", "unknown_fail_closed", "queue_unknown")
		case !*observation.Queue.AcceptingSubmissions.Value:
			add("queue_open", "fail", "queue_closed")
		default:
			add("queue_open", "pass", "")
		}
	}
	if target.ActivationModel == "on_demand" {
		if observation.Startup == nil || !observation.Startup.State.Known || !observation.Startup.ExpectedReadySeconds.Known {
			add("startup", "unknown_fail_closed", "startup_unknown")
		} else {
			add("startup", "pass", "")
		}
	}
	if policy.Spend.MaxCostMicros != nil {
		gates = append(gates, wayfinderSpendGate(workload, policy.Spend, candidate.Economics, observation))
	}
	return gates, nil
}

func wayfinderCapacityGate(target wayfinderExecutionTarget, observation wayfinderObservation) wayfinderGateResult {
	if target.CapacityModel == "unmetered" {
		return wayfinderGateResultValue("capacity", "pass", "capacity_unmetered_declared")
	}
	var values []wayfinderKnownUint64
	switch observation.Capacity.Model {
	case "concurrency_slots":
		if observation.Capacity.FreeSlots != nil {
			values = []wayfinderKnownUint64{*observation.Capacity.FreeSlots}
		}
	case "token_rate":
		if observation.Capacity.RemainingTokensPerMinute != nil && observation.Capacity.RemainingRequestsPerMinute != nil {
			values = []wayfinderKnownUint64{*observation.Capacity.RemainingTokensPerMinute, *observation.Capacity.RemainingRequestsPerMinute}
		}
	case "allocation_units":
		if observation.Capacity.AvailableUnits != nil {
			values = []wayfinderKnownUint64{*observation.Capacity.AvailableUnits}
		}
	}
	if len(values) == 0 {
		return wayfinderGateResultValue("capacity", "unknown_fail_closed", "capacity_unknown")
	}
	for _, value := range values {
		if !value.Known {
			return wayfinderGateResultValue("capacity", "unknown_fail_closed", "capacity_unknown")
		}
	}
	for _, value := range values {
		if *value.Value == 0 {
			return wayfinderGateResultValue("capacity", "fail", "capacity_exhausted")
		}
	}
	return wayfinderGateResultValue("capacity", "pass", "")
}

func wayfinderSpendGate(workload wayfinderWorkloadProfile, spend wayfinderSpendConstraint, economics wayfinderEconomics, observation wayfinderObservation) wayfinderGateResult {
	var cost *big.Int
	basis := "metered"
	if economics.Volatility == "variable" {
		if observation.LivePrice != nil && observation.LivePrice.UnitPriceMicros.Known {
			cost = new(big.Int).SetUint64(*observation.LivePrice.UnitPriceMicros.Value)
		}
	} else {
		switch economics.PricingModel {
		case "included_in_subscription":
			basis = "subscription"
			cost = new(big.Int)
		case "per_token":
			input := new(big.Int).Mul(new(big.Int).SetUint64(*economics.InputPriceMicros), new(big.Int).SetUint64(uint64(workload.ExpectedInputTokens)))
			input.Div(input, big.NewInt(1000))
			output := new(big.Int).Mul(new(big.Int).SetUint64(*economics.OutputPriceMicros), new(big.Int).SetUint64(uint64(workload.ExpectedOutputTokens)))
			output.Div(output, big.NewInt(1000))
			cost = new(big.Int).Add(input, output)
		case "per_second", "per_allocation_unit":
			cost = new(big.Int).SetUint64(*economics.UnitPriceMicros)
		}
	}
	if basis == "subscription" {
		return wayfinderGateResultValue("spend_ceiling", "pass", "")
	}
	if cost == nil {
		detail := "price_unknown"
		if economics.PricingModel == "unpriced" {
			detail = "price_unpriced"
		}
		return wayfinderGateResultValue("spend_ceiling", "unknown_fail_closed", detail)
	}
	if spend.Currency != nil && (economics.Currency == nil || *economics.Currency != *spend.Currency) {
		return wayfinderGateResultValue("spend_ceiling", "unknown_fail_closed", "currency_mismatch")
	}
	if cost.Cmp(new(big.Int).SetUint64(*spend.MaxCostMicros)) > 0 {
		return wayfinderGateResultValue("spend_ceiling", "fail", "spend_ceiling_exceeded")
	}
	return wayfinderGateResultValue("spend_ceiling", "pass", "")
}

func wayfinderGateResultValue(gate, result, detail string) wayfinderGateResult {
	value := wayfinderGateResult{Gate: gate, Result: result}
	if detail != "" {
		value.Detail = &detail
	}
	return value
}

func validateWayfinderDisposition(disposition wayfinderCandidateDisposition, expectedEvidence []wayfinderEvidenceRef) error {
	seenGates := make(map[string]struct{}, len(disposition.GateResults))
	for _, gate := range disposition.GateResults {
		if !wayfinderOneOf(gate.Gate, wayfinderGateNames...) || !wayfinderOneOf(gate.Result, "pass", "fail", "unknown_fail_closed") || (gate.Detail != nil && !wayfinderOneOf(*gate.Detail, wayfinderGateDetails...)) {
			return errors.New("invalid gate result")
		}
		if !wayfinderUniqueInsert(seenGates, gate.Gate) {
			return errors.New("duplicate gate result")
		}
		if disposition.Disposition == "selected" && gate.Result != "pass" {
			return fmt.Errorf("selected candidate gate %q did not pass", gate.Gate)
		}
	}
	if disposition.RankComponents != nil && !wayfinderValidRank(*disposition.RankComponents) {
		return errors.New("invalid rank components")
	}
	if len(disposition.EvidenceRefs) != len(expectedEvidence) {
		return errors.New("candidate evidence references are incomplete")
	}
	// Record IDs are scoped by their evidence domain in routing/v3. An
	// assessment, entitlement, and observation may therefore legitimately share
	// an ID. Match the complete immutable tuple as a multiset instead of
	// collapsing cross-domain records by RecordID.
	wantEvidence := make(map[wayfinderEvidenceRef]int, len(expectedEvidence))
	for _, evidence := range expectedEvidence {
		wantEvidence[evidence]++
	}
	for _, evidence := range disposition.EvidenceRefs {
		if wantEvidence[evidence] == 0 {
			return errors.New("candidate evidence reference is not bound to submitted evidence")
		}
		wantEvidence[evidence]--
		if wantEvidence[evidence] == 0 {
			delete(wantEvidence, evidence)
		}
	}
	if len(wantEvidence) != 0 {
		return errors.New("candidate evidence references are incomplete")
	}
	return nil
}

func wayfinderExpectedEvidenceRefs(request wayfinderEvaluateRequest, candidate wayfinderCandidate) []wayfinderEvidenceRef {
	refs := make([]wayfinderEvidenceRef, 0, 3)
	if candidate.ModelAssessmentRef != nil {
		for _, assessment := range request.ModelAssessments {
			if assessment.RecordID == *candidate.ModelAssessmentRef {
				refs = append(refs, wayfinderEvidenceRef{RecordID: assessment.RecordID, ObservedAtUnix: assessment.AssessedAtUnix, ExpiresAtUnix: assessment.ExpiresAtUnix})
				break
			}
		}
	}
	for _, entitlement := range request.Entitlements {
		if entitlement.RecordID == candidate.EntitlementRef {
			refs = append(refs, wayfinderEvidenceRef{RecordID: entitlement.RecordID, ObservedAtUnix: entitlement.ObservedAtUnix, ExpiresAtUnix: entitlement.ExpiresAtUnix})
			break
		}
	}
	for _, observation := range request.Observations {
		if observation.RecordID == candidate.ObservationRef {
			refs = append(refs, wayfinderEvidenceRef{RecordID: observation.RecordID, ObservedAtUnix: observation.ObservedAtUnix, ExpiresAtUnix: observation.ExpiresAtUnix})
			break
		}
	}
	return refs
}

var wayfinderGateNames = []string{
	"entitlement", "allocation", "residency", "data_handling", "provider_allowed",
	"input_modality", "output_modality", "context", "tools", "structured_output",
	"resource_profile", "reachable", "authenticated", "circuit", "throttle",
	"capacity", "queue_open", "startup", "spend_ceiling",
}

var wayfinderGateDetails = []string{
	"entitlement_unavailable", "entitlement_disabled", "authorization_suspended", "authorization_expired", "authorization_unknown", "billing_mode_unknown",
	"allocation_balance_missing", "allocation_balance_unknown", "allocation_exhausted", "residency_not_allowed", "local_only_required", "private_network_required",
	"provider_not_allowed", "input_modality_unsupported", "output_modality_unsupported", "context_below_minimum", "output_below_expected", "tools_unsupported",
	"structured_output_unsupported", "resource_profile_missing", "observation_unavailable", "not_reachable", "reachable_unknown", "not_authenticated", "authenticated_unknown",
	"circuit_open", "circuit_cooling", "circuit_unknown", "throttled_gateway", "throttled_upstream", "throttled_gateway_and_upstream", "throttle_unknown",
	"capacity_exhausted", "capacity_unknown", "capacity_unmetered_declared", "queue_closed", "queue_unknown", "startup_unknown", "spend_ceiling_exceeded",
	"price_unpriced", "price_unknown", "currency_mismatch",
}

func wayfinderValidRank(rank wayfinderRankComponents) bool {
	if rank.QualityScore > 100 || rank.LatencyScore > 100 || rank.CostScore > 100 || rank.WeightedScore > 10000 || !wayfinderOneOf(rank.CostBasis, "metered", "subscription", "unknown") || len(rank.UnknownInputs) > 3 {
		return false
	}
	seen := make(map[string]struct{}, len(rank.UnknownInputs))
	for _, input := range rank.UnknownInputs {
		if !wayfinderOneOf(input, "quality", "latency", "cost") || !wayfinderUniqueInsert(seen, input) {
			return false
		}
	}
	return true
}

func wayfinderRequestFingerprints(request wayfinderEvaluateRequest) wayfinderFingerprints {
	requestDomain := struct {
		CorrelationID string                   `json:"correlation_id"`
		Workload      wayfinderWorkloadProfile `json:"workload"`
		Constraints   wayfinderConstraints     `json:"constraints"`
		Objectives    wayfinderObjectives      `json:"objectives"`
		Preferences   wayfinderPreferences     `json:"preferences"`
	}{request.CorrelationID, request.Workload, request.Constraints, request.Objectives, request.Preferences}
	return wayfinderFingerprints{
		Request:         wayfinderHashJSON(wayfinderDomainRequest, requestDomain),
		Inventory:       wayfinderHashSorted(wayfinderDomainInventory, request.Candidates),
		ModelAssessment: wayfinderHashSorted(wayfinderDomainModelAssessment, request.ModelAssessments),
		AccountScope:    wayfinderHashSorted(wayfinderDomainAccountScope, request.Entitlements),
		Evidence:        wayfinderHashSorted(wayfinderDomainEvidence, request.Observations),
		Policy:          wayfinderHashJSON(wayfinderDomainPolicy, request.PolicyVersion),
		Clock:           wayfinderHashJSON(wayfinderDomainClock, request.NowUnix),
	}
}

func wayfinderDecisionID(fingerprints wayfinderFingerprints) string {
	fields := []string{fingerprints.Request, fingerprints.Inventory, fingerprints.ModelAssessment, fingerprints.AccountScope, fingerprints.Evidence, fingerprints.Policy, fingerprints.Clock}
	encoded, _ := json.Marshal(fields)
	return "routing/v3:" + wayfinderHashBytes(wayfinderDomainDecision, encoded)
}

func wayfinderHashJSON(domain string, value any) string {
	encoded, _ := json.Marshal(value)
	return "sha256:" + wayfinderHashBytes(domain, encoded)
}

func wayfinderHashSorted[T any](domain string, values []T) string {
	records := make([][]byte, 0, len(values))
	for _, value := range values {
		encoded, _ := json.Marshal(value)
		records = append(records, encoded)
	}
	sort.Slice(records, func(i, j int) bool { return bytes.Compare(records[i], records[j]) < 0 })
	var encoded bytes.Buffer
	encoded.WriteByte('[')
	for i, record := range records {
		if i > 0 {
			encoded.WriteByte(',')
		}
		encoded.WriteByte('[')
		for j, value := range record {
			if j > 0 {
				encoded.WriteByte(',')
			}
			encoded.WriteString(strconv.Itoa(int(value)))
		}
		encoded.WriteByte(']')
	}
	encoded.WriteByte(']')
	return "sha256:" + wayfinderHashBytes(domain, encoded.Bytes())
}

func wayfinderHashBytes(domain string, value []byte) string {
	hasher := sha256.New()
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(domain)))
	_, _ = hasher.Write(size[:])
	_, _ = hasher.Write([]byte(domain))
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = hasher.Write(size[:])
	_, _ = hasher.Write(value)
	return hex.EncodeToString(hasher.Sum(nil))
}

func wayfinderDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func canonicalizeWayfinderRequest(request *wayfinderEvaluateRequest) {
	sort.Slice(request.Candidates, func(i, j int) bool { return wayfinderJSONLess(request.Candidates[i], request.Candidates[j]) })
	sort.Slice(request.ModelAssessments, func(i, j int) bool {
		return wayfinderJSONLess(request.ModelAssessments[i], request.ModelAssessments[j])
	})
	sort.Slice(request.Entitlements, func(i, j int) bool { return wayfinderJSONLess(request.Entitlements[i], request.Entitlements[j]) })
	sort.Slice(request.Observations, func(i, j int) bool { return wayfinderJSONLess(request.Observations[i], request.Observations[j]) })
}

func wayfinderJSONLess(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Compare(leftJSON, rightJSON) < 0
}

func cloneWayfinderRequest(request wayfinderEvaluateRequest) (wayfinderEvaluateRequest, error) {
	var clone wayfinderEvaluateRequest
	encoded, err := json.Marshal(request)
	if err != nil {
		return clone, err
	}
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return clone, err
	}
	return clone, nil
}

func validWayfinderDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range []byte(value[len("sha256:"):]) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func wayfinderSafeString(value string) bool {
	if len(value) < 1 || len(value) > 256 || !wayfinderASCIIAlphaNumeric(value[0]) || strings.Contains(value, "://") || strings.ContainsAny(value, "?#=\n") {
		return false
	}
	for _, character := range []byte(value[1:]) {
		if !wayfinderASCIIAlphaNumeric(character) && !strings.ContainsRune("._:/-", rune(character)) {
			return false
		}
	}
	return !wayfinderLooksLikeCredential(value)
}

func wayfinderProviderID(value string) bool { return wayfinderFieldID(value, "provider") }
func wayfinderAccountRef(value string) bool { return wayfinderFieldID(value, "account") }
func wayfinderAdapterID(value string) bool  { return wayfinderFieldID(value, "adapter") }

func wayfinderFieldID(value, namespace string) bool {
	if !wayfinderSafeString(value) {
		return false
	}
	// Preserve routing/v3's legacy bare opaque IDs, but do not let a value
	// explicitly claiming one sensitive field namespace cross into another.
	if strings.Contains(value, "/") {
		for _, reserved := range []string{"provider", "account", "adapter"} {
			if strings.HasPrefix(value, reserved+"/") {
				return reserved == namespace && len(value) > len(reserved)+1
			}
		}
	}
	return true
}

func wayfinderLooksLikeCredential(value string) bool {
	lower := strings.ToLower(value)
	for _, prefix := range []string{"bearer-", "basic-"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for start := 0; start < len(lower); start++ {
		if start > 0 && !strings.ContainsRune("._:/-", rune(lower[start-1])) {
			continue
		}
		for _, prefix := range []string{"sk-", "rk-", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "xoxa-", "xoxb-", "xoxp-", "xoxr-", "xoxs-", "npm_", "pypi-", "hf_", "ya29."} {
			if strings.HasPrefix(lower[start:], prefix) {
				return true
			}
		}
		if wayfinderLooksLikeGoogleAPIKey(value[start:]) {
			return true
		}
	}
	for start := 0; start < len(value); start++ {
		if start > 0 && !strings.ContainsRune("._:/-", rune(value[start-1])) {
			continue
		}
		for _, prefix := range []string{"AKIA", "ASIA"} {
			if strings.HasPrefix(value[start:], prefix) && len(value)-start >= 20 {
				return true
			}
		}
	}
	for start := 0; start < len(value); start++ {
		if start == 0 || strings.ContainsRune("._:/-", rune(value[start-1])) {
			parts := strings.Split(value[start:], ".")
			if len(parts) == 3 && len(parts[0]) >= 8 && len(parts[1]) >= 8 && len(parts[2]) >= 4 && wayfinderJWTPart(parts[0]) && wayfinderJWTPart(parts[1]) && wayfinderJWTPart(parts[2]) {
				return true
			}
			if wayfinderLooksLikeUnprefixedCredential(value[start:]) {
				return true
			}
		}
	}
	return false
}

func wayfinderLooksLikeGoogleAPIKey(value string) bool {
	if len(value) < 39 || !strings.HasPrefix(value, "AIza") {
		return false
	}
	for _, character := range []byte(value[4:39]) {
		if !wayfinderASCIIAlphaNumeric(character) && character != '_' && character != '-' {
			return false
		}
	}
	return len(value) == 39 || strings.ContainsRune("._:/-", rune(value[39]))
}

func wayfinderLooksLikeUnprefixedCredential(value string) bool {
	// AWS secret access keys are unprefixed 40-byte base64 tokens. Limit this
	// fallback to that concrete shape; broad entropy guesses would reject normal
	// opaque base62 IDs even though their syntax is indistinguishable from keys.
	if len(value) != 40 {
		return false
	}
	var lower, upper, digits, slashes int
	for _, character := range []byte(value) {
		switch {
		case character >= 'a' && character <= 'z':
			lower++
		case character >= 'A' && character <= 'Z':
			upper++
		case character >= '0' && character <= '9':
			digits++
		case character == '/':
			slashes++
		default:
			return false
		}
	}
	// Mixed case plus a numeric or slash-bearing base64 component identifies the
	// credential shape without classifying lowercase hashes or account IDs.
	return lower >= 4 && upper >= 4 && (digits > 0 || slashes > 0)
}

func wayfinderASCIIAlphaNumeric(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func wayfinderJWTPart(value string) bool {
	for _, character := range []byte(value) {
		if !wayfinderASCIIAlphaNumeric(character) && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func wayfinderLooksLikeEndpoint(value string) bool {
	if separator := strings.LastIndexByte(value, ':'); separator >= 0 {
		port := value[separator+1:]
		if port != "" && len(port) <= 5 {
			allDigits := true
			for _, character := range []byte(port) {
				allDigits = allDigits && character >= '0' && character <= '9'
			}
			if allDigits {
				return true
			}
		}
	}
	parts := strings.Split(value, ".")
	if len(parts) == 4 {
		for _, part := range parts {
			if part == "" || len(part) > 3 {
				return false
			}
			for _, character := range []byte(part) {
				if character < '0' || character > '9' {
					return false
				}
			}
		}
		return true
	}
	return false
}

func wayfinderCanonicalModalities(values []string) bool {
	order := map[string]int{"text": 0, "image": 1, "audio": 2, "video": 3}
	if len(values) == 0 || len(values) > 4 {
		return false
	}
	previous := -1
	for _, value := range values {
		position, ok := order[value]
		if !ok || position <= previous {
			return false
		}
		previous = position
	}
	return true
}

func wayfinderValidKnownBool(value wayfinderKnownBool) bool {
	return value.Known == (value.Value != nil)
}

func wayfinderValidKnownString(value wayfinderKnownString, allowed ...string) bool {
	return value.Known == (value.Value != nil) && (!value.Known || wayfinderOneOf(*value.Value, allowed...))
}

func wayfinderValidKnownUint64(value wayfinderKnownUint64, positive bool) bool {
	return value.Known == (value.Value != nil) && (!positive || !value.Known || *value.Value > 0)
}

func wayfinderOneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func wayfinderUniqueSafe(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !wayfinderUniqueInsert(seen, value) {
			return false
		}
	}
	return true
}

func wayfinderUniqueInsert(seen map[string]struct{}, value string) bool {
	if !wayfinderSafeString(value) {
		return false
	}
	if _, exists := seen[value]; exists {
		return false
	}
	seen[value] = struct{}{}
	return true
}

func wayfinderOptionalSafe(value *string) bool {
	return value == nil || wayfinderSafeString(*value)
}

func wayfinderOptionalNonemptyUniqueSafe(values *[]string) bool {
	return values == nil || len(*values) > 0 && wayfinderUniqueSafe(*values)
}

func wayfinderUniqueProviderIDs(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !wayfinderProviderID(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func wayfinderOptionalNonemptyUniqueProviderIDs(values *[]string) bool {
	return values == nil || len(*values) > 0 && wayfinderUniqueProviderIDs(*values)
}

func wayfinderOptionalProviderID(value *string) bool {
	return value == nil || wayfinderProviderID(*value)
}

func wayfinderStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func wayfinderEntitlementFor(entitlements []wayfinderEntitlement, recordID string) (wayfinderEntitlement, bool) {
	for _, entitlement := range entitlements {
		if entitlement.RecordID == recordID {
			return entitlement, true
		}
	}
	return wayfinderEntitlement{}, false
}

func wayfinderObservationFor(observations []wayfinderObservation, recordID string) (wayfinderObservation, bool) {
	for _, observation := range observations {
		if observation.RecordID == recordID {
			return observation, true
		}
	}
	return wayfinderObservation{}, false
}

func wayfinderContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func wayfinderContainsAll(values, wanted []string) bool {
	for _, value := range wanted {
		if !wayfinderContains(values, value) {
			return false
		}
	}
	return true
}

func uniqueRecoveryTargets(targets []string) []string {
	result := make([]string, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, raw := range targets {
		target := strings.TrimSpace(raw)
		if target == "" {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		result = append(result, target)
	}
	return result
}

func readBoundedWayfinderPacket(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maximumWayfinderPacketBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maximumWayfinderPacketBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maximumWayfinderPacketBytes)
	}
	return body, nil
}

func recoveryWayfinderEvaluateEndpoint(raw string) (string, error) {
	baseURL := strings.TrimSpace(raw)
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("wayfinder URL must be an http loopback base URL with an explicit port")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("wayfinder URL must not contain user info")
	}
	if parsed.Scheme != "http" {
		return "", fmt.Errorf("wayfinder URL must use http")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("wayfinder URL must be a base URL without a path, query, or fragment")
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || port == "" {
		return "", fmt.Errorf("wayfinder URL must include an explicit port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("wayfinder URL must include a valid port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("wayfinder URL must use a numeric loopback address")
	}
	parsed.Path = wayfinderEvaluatePath
	return parsed.String(), nil
}
