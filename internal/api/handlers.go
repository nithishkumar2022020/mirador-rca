package api

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	rcav1 "github.com/miradorstack/mirador-rca/internal/grpc/generated"
	"github.com/miradorstack/mirador-rca/internal/models"
)

// FromProtoInvestigationRequest maps the gRPC request into a domain InvestigationRequest.
func FromProtoInvestigationRequest(req *rcav1.RCAInvestigationRequest) (models.InvestigationRequest, error) {
	if req == nil {
		return models.InvestigationRequest{}, fmt.Errorf("request is nil")
	}
	if req.TimeRange == nil || req.TimeRange.Start == nil || req.TimeRange.End == nil {
		return models.InvestigationRequest{}, fmt.Errorf("time_range.start and time_range.end are required")
	}

	start := req.TimeRange.Start.AsTime()
	end := req.TimeRange.End.AsTime()
	if start.IsZero() || end.IsZero() {
		return models.InvestigationRequest{}, fmt.Errorf("time_range values must be set")
	}

	return models.InvestigationRequest{
		IncidentID:       req.IncidentId,
		Symptoms:         append([]string(nil), req.Symptoms...),
		TimeRange:        models.TimeRange{Start: start, End: end},
		AffectedServices: append([]string(nil), req.AffectedServices...),
		AnomalyThreshold: req.AnomalyThreshold,
		TenantID:         req.TenantId,
	}, nil
}

// ToProtoCorrelationResult converts a domain result into the gRPC representation.
func ToProtoCorrelationResult(res models.CorrelationResult) *rcav1.CorrelationResult {
	proto := &rcav1.CorrelationResult{
		CorrelationId:    res.CorrelationID,
		IncidentId:       res.IncidentID,
		RootCause:        res.RootCause,
		Confidence:       res.Confidence,
		AffectedServices: append([]string(nil), res.AffectedServices...),
		Recommendations:  append([]string(nil), res.Recommendations...),
		CreatedAt:        timestamppb.New(res.CreatedAt),
		LlmSummary:       res.LLMSummary,
	}
	for _, anchor := range res.RedAnchors {
		proto.RedAnchors = append(proto.RedAnchors, &rcav1.RedAnchor{
			Service:      anchor.Service,
			Selector:     anchor.Selector,
			DataType:     toProtoDataType(anchor.DataType),
			Timestamp:    timestamppb.New(anchor.Timestamp),
			AnomalyScore: anchor.AnomalyScore,
			Threshold:    anchor.Threshold,
		})
	}
	for _, event := range res.Timeline {
		proto.Timeline = append(proto.Timeline, &rcav1.TimelineEvent{
			Time:         timestamppb.New(event.Time),
			Event:        event.Event,
			Service:      event.Service,
			Severity:     toProtoSeverity(event.Severity),
			AnomalyScore: event.AnomalyScore,
			DataSource:   toProtoDataType(event.DataSource),
		})
	}
	return proto
}

func toProtoDataType(dataType models.DataType) rcav1.DataType {
	switch dataType {
	case models.DataTypeMetrics:
		return rcav1.DataType_DATA_TYPE_METRICS
	case models.DataTypeLogs:
		return rcav1.DataType_DATA_TYPE_LOGS
	case models.DataTypeTraces:
		return rcav1.DataType_DATA_TYPE_TRACES
	default:
		return rcav1.DataType_DATA_TYPE_UNSPECIFIED
	}
}

func toProtoSeverity(sev models.Severity) rcav1.Severity {
	switch sev {
	case models.SeverityLow:
		return rcav1.Severity_SEVERITY_LOW
	case models.SeverityMedium:
		return rcav1.Severity_SEVERITY_MEDIUM
	case models.SeverityHigh:
		return rcav1.Severity_SEVERITY_HIGH
	case models.SeverityCritical:
		return rcav1.Severity_SEVERITY_CRITICAL
	default:
		return rcav1.Severity_SEVERITY_UNSPECIFIED
	}
}

// FromProtoFeedbackRequest converts the proto feedback into a domain struct.
func FromProtoFeedbackRequest(req *rcav1.FeedbackRequest) (models.Feedback, error) {
	if req == nil {
		return models.Feedback{}, fmt.Errorf("request is nil")
	}
	if req.GetCorrelationId() == "" {
		return models.Feedback{}, fmt.Errorf("correlation_id is required")
	}
	return models.Feedback{
		TenantID:      req.GetTenantId(),
		CorrelationID: req.GetCorrelationId(),
		Correct:       req.GetCorrect(),
		Notes:         req.GetNotes(),
		SubmittedAt:   time.Now().UTC(),
	}, nil
}

// FromProtoListCorrelationsRequest maps the proto request into a domain request.
func FromProtoListCorrelationsRequest(req *rcav1.ListCorrelationsRequest) (models.ListCorrelationsRequest, error) {
	if req == nil {
		return models.ListCorrelationsRequest{}, fmt.Errorf("request is nil")
	}

	var start, end time.Time
	if req.StartTime != nil {
		start = req.StartTime.AsTime()
	}
	if req.EndTime != nil {
		end = req.EndTime.AsTime()
	}

	return models.ListCorrelationsRequest{
		TenantID:  req.GetTenantId(),
		Service:   req.GetService(),
		Start:     start,
		End:       end,
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	}, nil
}

// ToProtoListCorrelationsResponse converts a domain list response into the proto shape.
func ToProtoListCorrelationsResponse(resp models.ListCorrelationsResponse) *rcav1.ListCorrelationsResponse {
	proto := &rcav1.ListCorrelationsResponse{NextPageToken: resp.NextPageToken}
	for _, corr := range resp.Correlations {
		proto.Correlations = append(proto.Correlations, ToProtoCorrelationResult(corr))
	}
	return proto
}

// ToProtoPatternsResponse maps failure patterns into the proto response.
func ToProtoPatternsResponse(patterns []models.FailurePattern) *rcav1.GetPatternsResponse {
	resp := &rcav1.GetPatternsResponse{}
	for _, pattern := range patterns {
		protoPattern := &rcav1.Pattern{
			Id:          pattern.ID,
			Name:        pattern.Name,
			Description: pattern.Description,
			Services:    append([]string(nil), pattern.Services...),
			Prevalence:  pattern.Prevalence,
		}
		if !pattern.LastSeen.IsZero() {
			protoPattern.LastSeen = timestamppb.New(pattern.LastSeen)
		}
		for _, anchor := range pattern.AnchorTemplates {
			protoPattern.AnchorTemplates = append(protoPattern.AnchorTemplates, &rcav1.AnchorTemplate{
				Service:        anchor.Service,
				SignalType:     anchor.SignalType,
				Selector:       anchor.Selector,
				TypicalLeadLag: anchor.TypicalLag,
				Threshold:      anchor.Threshold,
			})
		}
		protoPattern.Quality = &rcav1.Quality{
			Precision: pattern.Precision,
			Recall:    pattern.Recall,
		}
		resp.Patterns = append(resp.Patterns, protoPattern)
	}
	return resp
}
