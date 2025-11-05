package models

import "time"

// CorrelationResult summarises investigation output.
type CorrelationResult struct {
	CorrelationID    string          `json:"correlation_id"`
	IncidentID       string          `json:"incident_id"`
	RootCause        string          `json:"root_cause"`
	Confidence       float64         `json:"confidence"`
	AffectedServices []string        `json:"affected_services"`
	RedAnchors       []RedAnchor     `json:"red_anchors"`
	Timeline         []TimelineEvent `json:"timeline"`
	Recommendations  []string        `json:"recommendations"`
	CreatedAt        time.Time       `json:"created_at"`
}

// RedAnchor highlights a strong anomaly linked to the root cause.
type RedAnchor struct {
	Service      string    `json:"service"`
	Selector     string    `json:"selector"`
	DataType     DataType  `json:"data_type"`
	Timestamp    time.Time `json:"timestamp"`
	AnomalyScore float64   `json:"anomaly_score"`
	Threshold    float64   `json:"threshold"`
}

// TimelineEvent records a notable progression during the incident window.
type TimelineEvent struct {
	Time         time.Time `json:"time"`
	Event        string    `json:"event"`
	Service      string    `json:"service"`
	Severity     Severity  `json:"severity"`
	AnomalyScore float64   `json:"anomaly_score"`
	DataSource   DataType  `json:"data_source"`
}

// DataType enumerates signal categories.
type DataType string

const (
	DataTypeMetrics DataType = "metrics"
	DataTypeLogs    DataType = "logs"
	DataTypeTraces  DataType = "traces"
)

// Severity captures impact levels.
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)
