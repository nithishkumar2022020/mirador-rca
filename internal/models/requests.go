package models

import "time"

// InvestigationRequest represents a mirador-core investigation call.
type InvestigationRequest struct {
	IncidentID       string    `json:"incident_id"`
	Symptoms         []string  `json:"symptoms"`
	TimeRange        TimeRange `json:"time_range"`
	AffectedServices []string  `json:"affected_services"`
	AnomalyThreshold float64   `json:"anomaly_threshold"`
	TenantID         string    `json:"tenant_id"`
}

// TimeRange bounds the signal window for analysis.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// ListCorrelationsRequest captures filters for historical correlations.
type ListCorrelationsRequest struct {
	TenantID  string    `json:"tenant_id"`
	Service   string    `json:"service"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	PageSize  int       `json:"page_size"`
	PageToken string    `json:"page_token"`
}

// ListCorrelationsResponse contains correlation history records and pagination state.
type ListCorrelationsResponse struct {
	Correlations  []CorrelationResult `json:"correlations"`
	NextPageToken string              `json:"next_page_token"`
}

// Feedback captures user feedback for a correlation result.
type Feedback struct {
	TenantID      string    `json:"tenant_id"`
	CorrelationID string    `json:"correlation_id"`
	Correct       bool      `json:"correct"`
	Notes         string    `json:"notes"`
	SubmittedAt   time.Time `json:"submitted_at"`
}
