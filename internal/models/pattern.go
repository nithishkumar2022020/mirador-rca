package models

import "time"

// FailurePattern represents a mined failure template stored in Weaviate.
type FailurePattern struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Description     string           `json:"description"`
	Services        []string         `json:"services"`
	AnchorTemplates []AnchorTemplate `json:"anchor_templates"`
	Prevalence      float64          `json:"prevalence"`
	LastSeen        time.Time        `json:"last_seen"`
	Precision       float64          `json:"precision"`
	Recall          float64          `json:"recall"`
}

// AnchorTemplate describes a recurring anomaly signature.
type AnchorTemplate struct {
	Service    string  `json:"service"`
	SignalType string  `json:"signal_type"`
	Selector   string  `json:"selector"`
	TypicalLag float64 `json:"typical_lead_lag"`
	Threshold  float64 `json:"threshold"`
}
