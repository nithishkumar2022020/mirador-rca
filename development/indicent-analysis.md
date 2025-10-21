# Incident Analysis: mirador-rca Investigation Flow

This document describes exactly what happens when mirador-core calls `mirador-rca` to investigate an incident over a given time window (`start`, `end`). It can be used as a runbook reference or onboarding cheat sheet.

---

## 1. Request Entry & Validation

1. The Telemetry platform (mirador-core) issues a gRPC request to the `InvestigateIncident` RPC.
2. `cmd/rca-engine/main.go` has already started the gRPC server via `internal/api/server.go`.
3. The server dispatches the request to `internal/services/rca_service.go#InvestigateIncident`.
4. The service validates the proto—`internal/api/handlers.go#FromProtoInvestigationRequest` checks:
   - `incident_id` set
   - `tenant_id` set
   - `time_range.start` and `.end` present and valid RFC3339
   - optional fields (symptoms, affected services, threshold) handled
5. Invalid inputs result in a gRPC `InvalidArgument` error returned immediately.

## 2. Service Graph Acquisition (Topology)

1. `RCAService` passes the domain request to `internal/engine/pipeline.go#Investigate`.
2. The pipeline first requests the service dependency graph:
   - Calls `internal/repo/mirador_core.go#FetchServiceGraph`
   - Hits mirador-core’s `/api/v1/rca/service-graph` endpoint, which must proxy OTel `servicegraphconnector` metrics.
3. If the connector or endpoint is misconfigured, the pipeline aborts the investigation with an error—there is no synthetic fallback.
4. **Mandatory prerequisite:** OTel Collector with `servicegraphconnector` must be deployed and feeding mirador-core.

## 3. Signal Retrieval (Metrics, Logs, Traces)

1. The pipeline then pulls raw signals via mirador-core helper APIs (all respond with JSON):
   - `FetchMetricSeries` → `/api/v1/rca/metrics`
   - `FetchLogEntries` → `/api/v1/rca/logs`
   - `FetchTraceSpans` → `/api/v1/rca/traces`
2. Each request includes tenant, service (initially the first affected service, fallback to symptoms/`unknown-service` if none provided), and time window.
3. Any error, timeout, or empty response causes the investigation to fail—mirador-rca no longer manufactures synthetic samples.

## 4. Anomaly Extraction

1. Metric anomalies: `internal/extractors/metrics.go` (z-score over the window).
2. Log anomalies: `internal/extractors/logs.go` (median absolute deviation, error spike heuristics).
3. Trace anomalies: `internal/extractors/traces.go` (duration z-score + explicit error spans).

## 5. Anchor & Timeline Construction

1. Convert anomalies into `models.RedAnchor` objects with scores and thresholds.
2. Build a chronological timeline (`models.TimelineEvent`) from anomalies.
3. Append service-graph context (`appendTopologyEvents`): adds events for top upstream/downstream edges (call rate/error rate summary) and expands affected services to include neighbors via `neighborServices`.

## 6. Scoring & Root Cause

1. Confidence is computed as a weighted combination of metric/log/trace anomaly strength (`computeConfidence`).
2. Root cause starts as the top anchor (`deriveRootCause`), but the causality engine can override it when an upstream dependency consistently precedes the symptoms.

## 7. Recommendations

1. Attempt to recall similar incidents from Weaviate via `WeaviateRepo.SimilarIncidents`.
2. If no recommendations returned or call fails, fallback to rule-based suggestions (`RuleEngine` reading `configs/rules/default.yaml`).

## 8. Persistence & Response Assembly

1. Correlation result persisted for history (`WeaviateRepo.StoreCorrelation`).
2. The pipeline returns `models.CorrelationResult` to the service, containing:
   - Correlation ID, incident ID, root cause, confidence
   - Red anchors (top anomalies)
   - Timeline (anomaly chronology + service graph notes)
   - Recommendations
   - Affected services (including topology-aware neighbors)
3. `RCAService` converts it back to proto (`ToProtoCorrelationResult`).

## 9. Telemetry & Latency Tracking

1. `internal/utils/latency.go` tracks investigation durations (`latencies` in `RCAService`).
2. Every 20 samples, service logs the current p95 latency for observability.

## 10. Response to Caller

1. gRPC response is returned to mirador-core.
2. mirador-core merges this with additional UI data (timeline charts, log snippets).
3. RCA completes—operators view the result in Mirador UI.

---

## Prerequisites & Operational Notes

- **Service Graph**: Ensure OTel `servicegraphconnector` is deployed and its metrics are reachable. Misconfiguration causes investigations to fail outright.
- **mirador-core Config**: Must expose `/api/v1/rca/service-graph` alongside metrics/logs/traces endpoints—if any endpoint returns empty data, the investigation is rejected.
- **Weaviate**: External cluster required for history, patterns, and recommendation fallback.
- **Latency Targets**: Monitor the logged p95; adjust extractor thresholds if >4s.
- **Pattern Miner**: Offline job should run regularly to populate FailurePattern objects stored via `WeaviateRepo.StorePatterns`.

This pipeline matches the current repository implementation (`internal/engine/pipeline.go`, `internal/repo/mirador_core.go`, `internal/services/rca_service.go`) as of the latest development state.
