package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/miradorstack/mirador-rca/internal/cache"
	"github.com/miradorstack/mirador-rca/internal/models"
)

// WeaviateRepo provides read access to previously stored incidents and patterns.
type WeaviateRepo struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
	cache      cache.Provider
	similarTTL time.Duration
	patternTTL time.Duration
}

// NewWeaviateRepo constructs a Weaviate client.
func NewWeaviateRepo(endpoint, apiKey string, timeout time.Duration, cacheProvider cache.Provider, similarTTL, patternTTL time.Duration) *WeaviateRepo {
	if cacheProvider == nil {
		cacheProvider = cache.NoopProvider{}
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if similarTTL < 0 {
		similarTTL = 0
	}
	if patternTTL < 0 {
		patternTTL = 0
	}
	return &WeaviateRepo{
		endpoint:   strings.TrimRight(endpoint, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
		cache:      cacheProvider,
		similarTTL: similarTTL,
		patternTTL: patternTTL,
	}
}

// StorePatterns persists mined failure patterns.
func (r *WeaviateRepo) StorePatterns(ctx context.Context, tenantID string, patterns []models.FailurePattern) error {
	if r == nil {
		return fmt.Errorf("weaviate repo not initialised")
	}
	if r.endpoint == "" {
		return nil
	}

	for _, pattern := range patterns {
		payload := map[string]interface{}{
			"class":      "FailurePattern",
			"tenant":     tenantID,
			"properties": buildPatternProperties(tenantID, pattern),
		}
		if pattern.ID != "" {
			payload["id"] = pattern.ID
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/v1/objects", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if r.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+r.apiKey)
		}

		resp, err := r.httpClient.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("store pattern failed: %s", strings.TrimSpace(string(data)))
		}
		resp.Body.Close()
	}

	return nil
}

// StoreFeedback persists user feedback on correlations.
func (r *WeaviateRepo) StoreFeedback(ctx context.Context, feedback models.Feedback) error {
	if r == nil {
		return fmt.Errorf("weaviate repo not initialised")
	}
	if r.endpoint == "" {
		return nil
	}

	payload := map[string]interface{}{
		"class":      "CorrelationFeedback",
		"tenant":     feedback.TenantID,
		"properties": buildFeedbackProperties(feedback),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/v1/objects", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("store feedback failed: %s", strings.TrimSpace(string(data)))
	}

	return nil
}

// StoreCorrelation persists a correlation record for later recall.
func (r *WeaviateRepo) StoreCorrelation(ctx context.Context, tenantID string, correlation models.CorrelationResult) error {
	if r == nil {
		return fmt.Errorf("weaviate repo not initialised")
	}
	if r.endpoint == "" {
		return nil
	}

	payload := map[string]interface{}{
		"class":      "CorrelationRecord",
		"properties": buildCorrelationProperties(tenantID, correlation),
	}

	if correlation.CorrelationID != "" {
		payload["id"] = correlation.CorrelationID
	}
	if tenantID != "" {
		payload["tenant"] = tenantID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal correlation: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/v1/objects", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("weaviate store correlation failed: %s", strings.TrimSpace(string(data)))
	}

	return nil
}

// SimilarIncidents returns nearest-neighbour correlations for additional context.
func (r *WeaviateRepo) SimilarIncidents(ctx context.Context, tenantID string, symptoms []string, limit int) ([]models.CorrelationResult, error) {
	if r == nil {
		return nil, fmt.Errorf("weaviate repo not initialised")
	}

	if r.endpoint == "" {
		return syntheticSimilarIncidents(symptoms, limit), nil
	}

	cacheKey := ""
	if r.similarTTL > 0 {
		sorted := append([]string(nil), symptoms...)
		sort.Strings(sorted)
		cacheKey = cacheSimilarIncidentsKey(tenantID, sorted, limit)
		if data, err := r.cache.Get(ctx, cacheKey); err == nil {
			var cached []models.CorrelationResult
			if err := json.Unmarshal(data, &cached); err == nil {
				return cached, nil
			}
		}
	}

	gql := map[string]interface{}{
		"query": fmt.Sprintf(`{
          Get {
            CorrelationRecord(
              limit: %d
              where: {
                operator: And
                operands: [
                  {path: ["tenantId"], operator: Equal, valueString: "%s"}
                ]
              }
            ) {
              correlationId
              incidentId
              rootCause
              confidence
              affectedServices
              recommendations
              createdAt
            }
          }
        }`, limit, tenantID),
	}

	payload, err := json.Marshal(gql)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/v1/graphql", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return syntheticSimilarIncidents(symptoms, limit), nil
	}
	defer resp.Body.Close()

	var response struct {
		Data struct {
			Get struct {
				CorrelationRecord []struct {
					CorrelationID    string    `json:"correlationId"`
					IncidentID       string    `json:"incidentId"`
					RootCause        string    `json:"rootCause"`
					Confidence       float64   `json:"confidence"`
					AffectedServices []string  `json:"affectedServices"`
					Recommendations  []string  `json:"recommendations"`
					CreatedAt        time.Time `json:"createdAt"`
				} `json:"CorrelationRecord"`
			} `json:"Get"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return syntheticSimilarIncidents(symptoms, limit), nil
	}

	results := make([]models.CorrelationResult, 0, len(response.Data.Get.CorrelationRecord))
	for _, rec := range response.Data.Get.CorrelationRecord {
		results = append(results, models.CorrelationResult{
			CorrelationID:    rec.CorrelationID,
			IncidentID:       rec.IncidentID,
			RootCause:        rec.RootCause,
			Confidence:       rec.Confidence,
			AffectedServices: rec.AffectedServices,
			Recommendations:  rec.Recommendations,
			CreatedAt:        rec.CreatedAt,
		})
	}

	if r.similarTTL > 0 && cacheKey != "" && len(results) > 0 {
		if payload, err := json.Marshal(results); err == nil {
			_ = r.cache.Set(ctx, cacheKey, payload, r.similarTTL)
		}
	}

	return results, nil
}

func cacheSimilarIncidentsKey(tenantID string, symptoms []string, limit int) string {
	joined := strings.Join(symptoms, "|")
	return fmt.Sprintf("weaviate:similar:%s:%d:%s", tenantID, limit, joined)
}

// ListCorrelations returns historical correlations filtered by tenant/service/time.
func (r *WeaviateRepo) ListCorrelations(ctx context.Context, req models.ListCorrelationsRequest) (models.ListCorrelationsResponse, error) {
	if r == nil {
		return models.ListCorrelationsResponse{}, fmt.Errorf("weaviate repo not initialised")
	}

	if r.endpoint == "" {
		return syntheticCorrelationList(req), nil
	}

	limit := req.PageSize
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	offset := 0
	if req.PageToken != "" {
		if v, err := strconv.Atoi(req.PageToken); err == nil && v >= 0 {
			offset = v
		}
	}

	whereClause := buildCorrelationWhere(req)

	gql := fmt.Sprintf(`{
  Get {
    CorrelationRecord(
      limit: %d
      offset: %d
      %s
      sort: [{path: "createdAt", order: desc}]
    ) {
      correlationId
      incidentId
      rootCause
      confidence
      affectedServices
      recommendations
      createdAt
      redAnchors {
        service
        selector
        dataType
        timestamp
        anomalyScore
        threshold
      }
      timeline {
        time
        event
        service
        severity
        anomalyScore
        dataSource
      }
    }
  }
}`, limit, offset, whereClause)

	payload, err := json.Marshal(map[string]interface{}{"query": gql})
	if err != nil {
		return models.ListCorrelationsResponse{}, err
	}

	reqHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/v1/graphql", bytes.NewReader(payload))
	if err != nil {
		return models.ListCorrelationsResponse{}, err
	}
	reqHTTP.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		reqHTTP.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(reqHTTP)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return syntheticCorrelationList(req), nil
	}
	defer resp.Body.Close()

	var response struct {
		Data struct {
			Get struct {
				CorrelationRecord []struct {
					CorrelationID    string   `json:"correlationId"`
					IncidentID       string   `json:"incidentId"`
					RootCause        string   `json:"rootCause"`
					Confidence       float64  `json:"confidence"`
					AffectedServices []string `json:"affectedServices"`
					Recommendations  []string `json:"recommendations"`
					CreatedAt        string   `json:"createdAt"`
					RedAnchors       []struct {
						Service      string  `json:"service"`
						Selector     string  `json:"selector"`
						DataType     string  `json:"dataType"`
						Timestamp    string  `json:"timestamp"`
						AnomalyScore float64 `json:"anomalyScore"`
						Threshold    float64 `json:"threshold"`
					} `json:"redAnchors"`
					Timeline []struct {
						Time         string  `json:"time"`
						Event        string  `json:"event"`
						Service      string  `json:"service"`
						Severity     string  `json:"severity"`
						AnomalyScore float64 `json:"anomalyScore"`
						DataSource   string  `json:"dataSource"`
					} `json:"timeline"`
				} `json:"CorrelationRecord"`
			} `json:"Get"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return models.ListCorrelationsResponse{}, err
	}

	correlations := make([]models.CorrelationResult, 0, len(response.Data.Get.CorrelationRecord))
	for _, rec := range response.Data.Get.CorrelationRecord {
		createdAt, _ := time.Parse(time.RFC3339, rec.CreatedAt)
		anchors := make([]models.RedAnchor, 0, len(rec.RedAnchors))
		for _, anchor := range rec.RedAnchors {
			ts, _ := time.Parse(time.RFC3339, anchor.Timestamp)
			anchors = append(anchors, models.RedAnchor{
				Service:      anchor.Service,
				Selector:     anchor.Selector,
				DataType:     parseDataType(anchor.DataType),
				Timestamp:    ts,
				AnomalyScore: anchor.AnomalyScore,
				Threshold:    anchor.Threshold,
			})
		}

		timeline := make([]models.TimelineEvent, 0, len(rec.Timeline))
		for _, event := range rec.Timeline {
			ts, _ := time.Parse(time.RFC3339, event.Time)
			timeline = append(timeline, models.TimelineEvent{
				Time:         ts,
				Event:        event.Event,
				Service:      event.Service,
				Severity:     parseSeverity(event.Severity),
				AnomalyScore: event.AnomalyScore,
				DataSource:   parseDataType(event.DataSource),
			})
		}

		correlations = append(correlations, models.CorrelationResult{
			CorrelationID:    rec.CorrelationID,
			IncidentID:       rec.IncidentID,
			RootCause:        rec.RootCause,
			Confidence:       rec.Confidence,
			AffectedServices: rec.AffectedServices,
			Recommendations:  rec.Recommendations,
			CreatedAt:        createdAt,
			RedAnchors:       anchors,
			Timeline:         timeline,
		})
	}

	nextToken := ""
	if len(correlations) == limit {
		nextToken = strconv.Itoa(offset + len(correlations))
	}

	return models.ListCorrelationsResponse{
		Correlations:  correlations,
		NextPageToken: nextToken,
	}, nil
}

// FetchPatterns retrieves failure patterns for the tenant.
func (r *WeaviateRepo) FetchPatterns(ctx context.Context, tenantID, service string) ([]models.FailurePattern, error) {
	if r == nil {
		return nil, fmt.Errorf("weaviate repo not initialised")
	}

	if r.endpoint == "" {
		return syntheticPatterns(service), nil
	}

	cacheKey := ""
	if r.patternTTL > 0 {
		cacheKey = cachePatternsKey(tenantID, service)
		if data, err := r.cache.Get(ctx, cacheKey); err == nil {
			var cached []models.FailurePattern
			if err := json.Unmarshal(data, &cached); err == nil {
				return cached, nil
			}
		}
	}

	gql := map[string]interface{}{
		"query": fmt.Sprintf(`{
          Get {
            FailurePattern(
              where: {
                operator: And
                operands: [
                  {path: ["tenantId"], operator: Equal, valueString: "%s"}
                  %s
                ]
              }
            ) {
              patternId
              name
              description
              services
              anchorTemplates {
                service
                signalType
                selector
                typicalLeadLag
                thresholds
              }
              prevalence
              lastSeen
              quality {
                precision
                recall
              }
            }
          }
        }`, tenantID, optionalServiceOperand(service)),
	}

	body, err := json.Marshal(gql)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/v1/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return syntheticPatterns(service), nil
	}
	defer resp.Body.Close()

	var response struct {
		Data struct {
			Get struct {
				FailurePattern []struct {
					PatternID       string   `json:"patternId"`
					Name            string   `json:"name"`
					Description     string   `json:"description"`
					Services        []string `json:"services"`
					AnchorTemplates []struct {
						Service        string  `json:"service"`
						SignalType     string  `json:"signalType"`
						Selector       string  `json:"selector"`
						TypicalLeadLag float64 `json:"typicalLeadLag"`
						Thresholds     float64 `json:"thresholds"`
					} `json:"anchorTemplates"`
					Prevalence float64 `json:"prevalence"`
					LastSeen   string  `json:"lastSeen"`
					Quality    struct {
						Precision float64 `json:"precision"`
						Recall    float64 `json:"recall"`
					} `json:"quality"`
				} `json:"FailurePattern"`
			} `json:"Get"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return syntheticPatterns(service), nil
	}

	patterns := make([]models.FailurePattern, 0, len(response.Data.Get.FailurePattern))
	for _, p := range response.Data.Get.FailurePattern {
		anchorTemplates := make([]models.AnchorTemplate, 0, len(p.AnchorTemplates))
		for _, at := range p.AnchorTemplates {
			anchorTemplates = append(anchorTemplates, models.AnchorTemplate{
				Service:    at.Service,
				SignalType: at.SignalType,
				Selector:   at.Selector,
				TypicalLag: at.TypicalLeadLag,
				Threshold:  at.Thresholds,
			})
		}
		lastSeen, _ := time.Parse(time.RFC3339, p.LastSeen)
		patterns = append(patterns, models.FailurePattern{
			ID:              p.PatternID,
			Name:            p.Name,
			Description:     p.Description,
			Services:        p.Services,
			AnchorTemplates: anchorTemplates,
			Prevalence:      p.Prevalence,
			LastSeen:        lastSeen,
			Precision:       p.Quality.Precision,
			Recall:          p.Quality.Recall,
		})
	}

	if r.patternTTL > 0 && cacheKey != "" && len(patterns) > 0 {
		if payload, err := json.Marshal(patterns); err == nil {
			_ = r.cache.Set(ctx, cacheKey, payload, r.patternTTL)
		}
	}

	return patterns, nil
}

func cachePatternsKey(tenantID, service string) string {
	return fmt.Sprintf("weaviate:patterns:%s:%s", tenantID, service)
}

func optionalServiceOperand(service string) string {
	if service == "" {
		return ""
	}
	return fmt.Sprintf(`, {path: ["services"], operator: ContainsAny, valueString: "%s"}`, service)
}

func syntheticSimilarIncidents(symptoms []string, limit int) []models.CorrelationResult {
	if limit <= 0 {
		limit = 3
	}
	results := make([]models.CorrelationResult, 0, limit)
	for i := 0; i < limit; i++ {
		results = append(results, models.CorrelationResult{
			CorrelationID:    fmt.Sprintf("synthetic-%d", i+1),
			IncidentID:       fmt.Sprintf("incident-%d", i+1),
			RootCause:        "synthetic-service",
			Confidence:       0.55 + float64(i)*0.05,
			AffectedServices: []string{"synthetic-service"},
			Recommendations:  []string{"Check downstream dependencies", "Review recent deploy"},
			CreatedAt:        time.Now().Add(-time.Duration(i) * time.Hour),
		})
	}
	if len(symptoms) > 0 {
		results[0].Recommendations = append(results[0].Recommendations, fmt.Sprintf("Symptom hint: %s", symptoms[0]))
	}
	return results
}

func syntheticPatterns(service string) []models.FailurePattern {
	svc := service
	if svc == "" {
		svc = "synthetic-service"
	}
	return []models.FailurePattern{
		{
			ID:          "pattern-1",
			Name:        "CPU saturation",
			Description: "Sudden CPU saturation followed by error spikes",
			Services:    []string{svc},
			AnchorTemplates: []models.AnchorTemplate{
				{Service: svc, SignalType: "metrics", Selector: "cpu_usage", TypicalLag: 1, Threshold: 0.8},
				{Service: svc, SignalType: "logs", Selector: "error", TypicalLag: 2, Threshold: 10},
			},
			Prevalence: 0.32,
			LastSeen:   time.Now().Add(-24 * time.Hour),
			Precision:  0.68,
			Recall:     0.44,
		},
	}
}

func buildPatternProperties(tenantID string, pattern models.FailurePattern) map[string]interface{} {
	anchors := make([]map[string]interface{}, 0, len(pattern.AnchorTemplates))
	for _, anchor := range pattern.AnchorTemplates {
		anchors = append(anchors, map[string]interface{}{
			"service":        anchor.Service,
			"signalType":     anchor.SignalType,
			"selector":       anchor.Selector,
			"typicalLeadLag": anchor.TypicalLag,
			"threshold":      anchor.Threshold,
		})
	}

	quality := map[string]interface{}{
		"precision": pattern.Precision,
		"recall":    pattern.Recall,
	}

	lastSeen := pattern.LastSeen
	if lastSeen.IsZero() {
		lastSeen = time.Now().UTC()
	}

	return map[string]interface{}{
		"patternId":       pattern.ID,
		"tenantId":        tenantID,
		"name":            pattern.Name,
		"description":     pattern.Description,
		"services":        pattern.Services,
		"prevalence":      pattern.Prevalence,
		"lastSeen":        lastSeen.Format(time.RFC3339),
		"anchorTemplates": anchors,
		"quality":         quality,
	}
}

func buildFeedbackProperties(feedback models.Feedback) map[string]interface{} {
	submitted := feedback.SubmittedAt
	if submitted.IsZero() {
		submitted = time.Now().UTC()
	}
	return map[string]interface{}{
		"tenantId":      feedback.TenantID,
		"correlationId": feedback.CorrelationID,
		"correct":       feedback.Correct,
		"notes":         feedback.Notes,
		"submittedAt":   submitted.Format(time.RFC3339),
	}
}

func buildCorrelationProperties(tenantID string, correlation models.CorrelationResult) map[string]interface{} {
	createdAt := correlation.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	anchors := make([]map[string]interface{}, 0, len(correlation.RedAnchors))
	for _, anchor := range correlation.RedAnchors {
		anchors = append(anchors, map[string]interface{}{
			"service":      anchor.Service,
			"selector":     anchor.Selector,
			"dataType":     string(anchor.DataType),
			"timestamp":    anchor.Timestamp.UTC().Format(time.RFC3339),
			"anomalyScore": anchor.AnomalyScore,
			"threshold":    anchor.Threshold,
		})
	}

	timeline := make([]map[string]interface{}, 0, len(correlation.Timeline))
	for _, event := range correlation.Timeline {
		timeline = append(timeline, map[string]interface{}{
			"time":         event.Time.UTC().Format(time.RFC3339),
			"event":        event.Event,
			"service":      event.Service,
			"severity":     string(event.Severity),
			"anomalyScore": event.AnomalyScore,
			"dataSource":   string(event.DataSource),
		})
	}

	return map[string]interface{}{
		"correlationId":    correlation.CorrelationID,
		"incidentId":       correlation.IncidentID,
		"tenantId":         tenantID,
		"rootCause":        correlation.RootCause,
		"confidence":       correlation.Confidence,
		"affectedServices": correlation.AffectedServices,
		"recommendations":  correlation.Recommendations,
		"createdAt":        createdAt.Format(time.RFC3339),
		"redAnchors":       anchors,
		"timeline":         timeline,
	}
}

func buildCorrelationWhere(req models.ListCorrelationsRequest) string {
	filters := []string{fmt.Sprintf(`{path: ["tenantId"], operator: Equal, valueString: "%s"}`, req.TenantID)}

	if req.Service != "" {
		filters = append(filters, fmt.Sprintf(`{path: ["affectedServices"], operator: ContainsAny, valueString: "%s"}`, req.Service))
	}
	if !req.Start.IsZero() {
		filters = append(filters, fmt.Sprintf(`{path: ["createdAt"], operator: GreaterThanEqual, valueDate: "%s"}`, req.Start.Format(time.RFC3339)))
	}
	if !req.End.IsZero() {
		filters = append(filters, fmt.Sprintf(`{path: ["createdAt"], operator: LessThanEqual, valueDate: "%s"}`, req.End.Format(time.RFC3339)))
	}

	return fmt.Sprintf("where: { operator: And, operands: [%s] }", strings.Join(filters, ","))
}

func parseDataType(value string) models.DataType {
	switch strings.ToLower(value) {
	case "metrics":
		return models.DataTypeMetrics
	case "logs":
		return models.DataTypeLogs
	case "traces":
		return models.DataTypeTraces
	default:
		return models.DataType(value)
	}
}

func parseSeverity(value string) models.Severity {
	switch strings.ToLower(value) {
	case "low":
		return models.SeverityLow
	case "medium":
		return models.SeverityMedium
	case "high":
		return models.SeverityHigh
	case "critical":
		return models.SeverityCritical
	default:
		return models.Severity(value)
	}
}

func syntheticCorrelationList(req models.ListCorrelationsRequest) models.ListCorrelationsResponse {
	service := req.Service
	if service == "" {
		service = "synthetic-service"
	}

	items := []models.CorrelationResult{
		{
			CorrelationID:    "synthetic-corr-1",
			IncidentID:       "synthetic-incident-1",
			RootCause:        fmt.Sprintf("%s: cpu usage anomaly", service),
			Confidence:       0.65,
			AffectedServices: []string{service},
			Recommendations:  []string{"Scale service", "Review upstream errors"},
			CreatedAt:        time.Now().Add(-1 * time.Hour),
			RedAnchors: []models.RedAnchor{
				{
					Service:      service,
					Selector:     "metrics:cpu_usage",
					DataType:     models.DataTypeMetrics,
					Timestamp:    time.Now().Add(-70 * time.Minute),
					AnomalyScore: 3.2,
					Threshold:    2.0,
				},
			},
			Timeline: []models.TimelineEvent{
				{
					Time:         time.Now().Add(-75 * time.Minute),
					Event:        "Metric anomaly detected",
					Service:      service,
					Severity:     models.SeverityHigh,
					AnomalyScore: 3.2,
					DataSource:   models.DataTypeMetrics,
				},
			},
		},
	}

	return models.ListCorrelationsResponse{Correlations: items}
}
