package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/miradorstack/mirador-rca/internal/models"
)

// RESTService defines the interface for REST API operations.
type RESTService interface {
	InvestigateIncidentREST(ctx context.Context, req models.InvestigationRequest) (models.CorrelationResult, error)
	ListCorrelationsREST(ctx context.Context, req models.ListCorrelationsRequest) (models.ListCorrelationsResponse, error)
	GetPatternsREST(ctx context.Context, tenantID, service string) ([]models.FailurePattern, error)
	SubmitFeedbackREST(ctx context.Context, feedback models.Feedback) error
	HealthCheckREST(ctx context.Context) string
}

// RESTHandler handles REST API requests.
type RESTHandler struct {
	service RESTService
	logger  *slog.Logger
}

// NewRESTHandler creates a new REST handler.
func NewRESTHandler(service RESTService, logger *slog.Logger) *RESTHandler {
	return &RESTHandler{
		service: service,
		logger:  logger,
	}
}

// InvestigateIncident handles POST /api/v1/investigate
func (h *RESTHandler) InvestigateIncident(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.InvestigationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	result, err := h.service.InvestigateIncidentREST(r.Context(), req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Investigation failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ListCorrelations handles GET /api/v1/correlations
func (h *RESTHandler) ListCorrelations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	req := models.ListCorrelationsRequest{
		TenantID:  r.URL.Query().Get("tenant_id"),
		Service:   r.URL.Query().Get("service"),
		PageToken: r.URL.Query().Get("page_token"),
	}
	if pageSizeStr := r.URL.Query().Get("page_size"); pageSizeStr != "" {
		if pageSize, err := strconv.Atoi(pageSizeStr); err == nil {
			req.PageSize = pageSize
		}
	}
	if startStr := r.URL.Query().Get("start"); startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			req.Start = t
		}
	}
	if endStr := r.URL.Query().Get("end"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			req.End = t
		}
	}

	resp, err := h.service.ListCorrelationsREST(r.Context(), req)
	if err != nil {
		http.Error(w, fmt.Sprintf("List correlations failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GetPatterns handles GET /api/v1/patterns
func (h *RESTHandler) GetPatterns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	service := r.URL.Query().Get("service")

	patterns, err := h.service.GetPatternsREST(r.Context(), tenantID, service)
	if err != nil {
		http.Error(w, fmt.Sprintf("Get patterns failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"patterns": patterns})
}

// SubmitFeedback handles POST /api/v1/feedback
func (h *RESTHandler) SubmitFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var feedback models.Feedback
	if err := json.NewDecoder(r.Body).Decode(&feedback); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	err := h.service.SubmitFeedbackREST(r.Context(), feedback)
	if err != nil {
		http.Error(w, fmt.Sprintf("Submit feedback failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"correlation_id": feedback.CorrelationID, "accepted": true})
}

// HealthCheck handles GET /health
func (h *RESTHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := h.service.HealthCheckREST(r.Context())

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}
