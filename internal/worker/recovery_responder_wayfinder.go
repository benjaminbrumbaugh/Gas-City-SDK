package worker

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	wayfinderEvaluatePath       = "/routing/v3/evaluate"
	wayfinderRequestHeaderValue = "operator-v1"
	maximumWayfinderPacketBytes = 1 << 20
)

// WayfinderRecoveryAdvisor asks the loopback Wayfinder routing/v3 evaluate
// endpoint for an advisory target. The operator-authored template remains the
// authority for inventory, evidence, entitlements, policy, and workload facts.
type WayfinderRecoveryAdvisor struct {
	endpoint string
	template wayfinderEvaluateRequest
	client   *http.Client
}

// wayfinderEvaluateRequest models the complete top-level routing/v3 packet.
// Opaque contract-owned values are retained verbatim so this adapter can update
// only the three caller-owned request values.
type wayfinderEvaluateRequest struct {
	SchemaVersion    string               `json:"schema_version"`
	CorrelationID    string               `json:"correlation_id"`
	Workload         json.RawMessage      `json:"workload"`
	Constraints      wayfinderConstraints `json:"constraints"`
	Objectives       json.RawMessage      `json:"objectives"`
	Preferences      json.RawMessage      `json:"preferences"`
	Candidates       []json.RawMessage    `json:"candidates"`
	ModelAssessments json.RawMessage      `json:"model_assessments"`
	Entitlements     json.RawMessage      `json:"entitlements"`
	Observations     json.RawMessage      `json:"observations"`
	NowUnix          int64                `json:"now_unix"`
	PolicyVersion    string               `json:"policy_version"`
}

type wayfinderConstraints struct {
	Policy json.RawMessage          `json:"policy"`
	Work   wayfinderWorkConstraints `json:"work"`
}

type wayfinderWorkConstraints struct {
	AllowedTargetIDs []string        `json:"allowed_target_ids"`
	ModelID          json.RawMessage `json:"model_id"`
	TargetID         json.RawMessage `json:"target_id"`
}

type wayfinderEvaluateResult struct {
	SchemaVersion            string                   `json:"schema_version"`
	CorrelationID            string                   `json:"correlation_id"`
	DecisionID               string                   `json:"decision_id"`
	Request                  json.RawMessage          `json:"request"`
	Disposition              string                   `json:"disposition"`
	Recommendation           *wayfinderRecommendation `json:"recommendation"`
	Candidates               json.RawMessage          `json:"candidates"`
	Fingerprints             json.RawMessage          `json:"fingerprints"`
	PolicyVersion            string                   `json:"policy_version"`
	IssuedAtUnix             int64                    `json:"issued_at_unix"`
	ExpiresAtUnix            int64                    `json:"expires_at_unix"`
	AdvisoryOnly             bool                     `json:"advisory_only"`
	NoActiveMigration        bool                     `json:"no_active_migration"`
	AlternativesAdvisoryOnly bool                     `json:"alternatives_advisory_only"`
	Reevaluation             json.RawMessage          `json:"reevaluation"`
}

type wayfinderRecommendation struct {
	CandidateID     string          `json:"candidate_id"`
	Model           json.RawMessage `json:"model"`
	ExecutionTarget struct {
		TargetID      string `json:"target_id"`
		ConfigDigest  string `json:"config_digest"`
		AdapterID     string `json:"adapter_id"`
		AdapterDigest string `json:"adapter_digest"`
	} `json:"execution_target"`
	BillingMode               string          `json:"billing_mode"`
	ProviderPreferenceMatched bool            `json:"provider_preference_matched"`
	ModelPreferenceMatched    bool            `json:"model_preference_matched"`
	RankComponents            json.RawMessage `json:"rank_components"`
}

type wayfinderCandidateBinding struct {
	CandidateID     string `json:"candidate_id"`
	ExecutionTarget struct {
		TargetID      string `json:"target_id"`
		ConfigDigest  string `json:"config_digest"`
		AdapterID     string `json:"adapter_id"`
		AdapterDigest string `json:"adapter_digest"`
	} `json:"execution_target"`
}

// NewWayfinderRecoveryAdvisor validates a loopback base URL and a complete
// operator-authored routing/v3 request template.
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
	return &WayfinderRecoveryAdvisor{endpoint: endpoint, template: packet, client: client}, nil
}

// Recommend submits the operator packet after narrowing it to configured,
// not-yet-attempted recovery targets. Callers own lifecycle actions.
func (a *WayfinderRecoveryAdvisor) Recommend(ctx context.Context, request RecoveryRequest) (string, error) {
	if a == nil || a.endpoint == "" {
		return "", fmt.Errorf("wayfinder endpoint is unavailable")
	}
	correlationID := strings.TrimSpace(request.CorrelationID)
	if correlationID == "" || request.Now.IsZero() {
		return "", fmt.Errorf("wayfinder recovery request requires correlation id and current time")
	}
	remaining := uniqueRecoveryTargets(request.Targets)
	if len(remaining) == 0 {
		return "", fmt.Errorf("wayfinder recovery request has no remaining targets")
	}
	remainingSet := make(map[string]struct{}, len(remaining))
	for _, target := range remaining {
		remainingSet[target] = struct{}{}
	}

	packet := a.template
	packet.CorrelationID = correlationID
	packet.NowUnix = request.Now.Unix()
	packet.Constraints.Work.AllowedTargetIDs = append([]string(nil), remaining...)
	packet.Candidates = packet.Candidates[:0:0]
	for _, candidate := range a.template.Candidates {
		targetID := candidateTargetID(candidate)
		if _, ok := remainingSet[targetID]; ok {
			packet.Candidates = append(packet.Candidates, append(json.RawMessage(nil), candidate...))
		}
	}
	if len(packet.Candidates) == 0 {
		return "", fmt.Errorf("wayfinder request template has no candidate for remaining recovery targets")
	}

	body, err := json.Marshal(packet)
	if err != nil {
		return "", fmt.Errorf("encode wayfinder routing/v3 request: %w", err)
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
		return "", fmt.Errorf("call wayfinder routing/v3 evaluate: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("wayfinder routing/v3 evaluate returned %s", response.Status)
	}

	responseBody, err := readBoundedWayfinderPacket(response.Body)
	if err != nil {
		return "", fmt.Errorf("read wayfinder routing/v3 response: %w", err)
	}
	var result wayfinderEvaluateResult
	if err := decodeSingleJSON(responseBody, &result, true); err != nil {
		return "", fmt.Errorf("decode wayfinder routing/v3 response: %w", err)
	}
	if result.SchemaVersion != "routing/v3" {
		return "", fmt.Errorf("wayfinder response schema_version is %q", result.SchemaVersion)
	}
	if result.CorrelationID != correlationID {
		return "", fmt.Errorf("wayfinder response correlation_id does not match request")
	}
	if result.Disposition != "selected" || result.Recommendation == nil {
		return "", fmt.Errorf("wayfinder response has no selected recommendation")
	}
	if !validWayfinderDecisionID(result.DecisionID) || result.IssuedAtUnix < 1 || result.ExpiresAtUnix <= request.Now.Unix() {
		return "", fmt.Errorf("wayfinder response identity or validity window is invalid")
	}
	if !result.AdvisoryOnly || !result.NoActiveMigration || !result.AlternativesAdvisoryOnly {
		return "", fmt.Errorf("wayfinder response is not advisory-only")
	}
	if !rawJSONPresent(result.Request) || !rawJSONPresent(result.Candidates) || !rawJSONPresent(result.Fingerprints) ||
		!rawJSONPresent(result.Reevaluation) || strings.TrimSpace(result.PolicyVersion) == "" ||
		!rawJSONPresent(result.Recommendation.Model) || !rawJSONPresent(result.Recommendation.RankComponents) ||
		strings.TrimSpace(result.Recommendation.BillingMode) == "" {
		return "", fmt.Errorf("wayfinder response is incomplete")
	}
	target := strings.TrimSpace(result.Recommendation.ExecutionTarget.TargetID)
	if _, ok := remainingSet[target]; !ok {
		return "", fmt.Errorf("wayfinder selected target %q outside remaining recovery targets", target)
	}
	if !recommendationMatchesCandidate(*result.Recommendation, packet.Candidates) {
		return "", fmt.Errorf("wayfinder recommendation does not match an offered candidate")
	}
	return target, nil
}

func validWayfinderDecisionID(value string) bool {
	const prefix = "routing/v3:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil && value == strings.ToLower(value)
}

func recommendationMatchesCandidate(recommendation wayfinderRecommendation, candidates []json.RawMessage) bool {
	for _, raw := range candidates {
		var candidate wayfinderCandidateBinding
		if err := json.Unmarshal(raw, &candidate); err != nil {
			continue
		}
		if strings.TrimSpace(candidate.CandidateID) == strings.TrimSpace(recommendation.CandidateID) &&
			strings.TrimSpace(candidate.ExecutionTarget.TargetID) == strings.TrimSpace(recommendation.ExecutionTarget.TargetID) &&
			candidate.ExecutionTarget.ConfigDigest == recommendation.ExecutionTarget.ConfigDigest &&
			candidate.ExecutionTarget.AdapterID == recommendation.ExecutionTarget.AdapterID &&
			candidate.ExecutionTarget.AdapterDigest == recommendation.ExecutionTarget.AdapterDigest {
			return true
		}
	}
	return false
}

func decodeWayfinderEvaluateTemplate(data []byte) (wayfinderEvaluateRequest, error) {
	var packet wayfinderEvaluateRequest
	if len(data) == 0 || len(data) > maximumWayfinderPacketBytes {
		return packet, fmt.Errorf("template must contain at most %d bytes", maximumWayfinderPacketBytes)
	}
	if err := decodeSingleJSON(data, &packet, true); err != nil {
		return packet, err
	}
	if packet.SchemaVersion != "routing/v3" {
		return packet, fmt.Errorf("schema_version must be routing/v3")
	}
	if strings.TrimSpace(packet.CorrelationID) == "" || packet.NowUnix < 1 || strings.TrimSpace(packet.PolicyVersion) == "" {
		return packet, fmt.Errorf("template is missing required correlation_id, now_unix, or policy_version")
	}
	if !rawJSONPresent(packet.Workload) || !rawJSONPresent(packet.Objectives) || !rawJSONPresent(packet.Preferences) ||
		!rawJSONPresent(packet.ModelAssessments) || !rawJSONPresent(packet.Entitlements) || !rawJSONPresent(packet.Observations) ||
		!rawJSONPresent(packet.Constraints.Policy) || len(packet.Constraints.Work.ModelID) == 0 || len(packet.Constraints.Work.TargetID) == 0 {
		return packet, fmt.Errorf("template is not a complete routing/v3 request packet")
	}
	if len(packet.Constraints.Work.AllowedTargetIDs) == 0 || len(packet.Candidates) == 0 {
		return packet, fmt.Errorf("template requires allowed targets and candidates")
	}
	for _, candidate := range packet.Candidates {
		if candidateTargetID(candidate) == "" {
			return packet, fmt.Errorf("template candidate is missing execution_target.target_id")
		}
	}
	return packet, nil
}

func decodeSingleJSON(data []byte, result any, disallowUnknown bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if disallowUnknown {
		decoder.DisallowUnknownFields()
	}
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
	return nil
}

func rawJSONPresent(value json.RawMessage) bool {
	return len(value) > 0 && string(value) != "null"
}

func candidateTargetID(raw json.RawMessage) string {
	var candidate struct {
		ExecutionTarget struct {
			TargetID string `json:"target_id"`
		} `json:"execution_target"`
	}
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return ""
	}
	return strings.TrimSpace(candidate.ExecutionTarget.TargetID)
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
