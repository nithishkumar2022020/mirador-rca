# mirador-rca

> Work-in-progress Root Cause Analysis service for the Mirador stack.

## Prerequisites
- Go 1.23+
- `protoc` with Go & gRPC plugins (`protoc-gen-go`, `protoc-gen-go-grpc`).
- External Weaviate cluster reachable from the service.
- mirador-core API access for metrics/logs/traces aggregation.
- **Mandatory:** Deploy the OpenTelemetry Collector [servicegraphconnector](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/connector/servicegraphconnector) and ensure its emitted service graph metrics are available. mirador-rca relies on this topology data to correlate anomalies across services; if the endpoint is missing or empty, investigations fail.
- Configure mirador-core to expose a service-graph endpoint (default `/api/v1/rca/service-graph`) that proxies the connector metrics so mirador-rca can fetch the dependency topology prior to each investigation.
- mirador-rca performs no synthetic fallbacks—metrics, logs, traces, and service graph data **must** be returned by mirador-core for investigations to succeed.
- See `docs/phase3-eval.md` for precision@1 evaluation details and `docs/indicent-analysis.md` for a deep dive of the pipeline.

## Quickstart
```
make fmt            # gofmt + goimports all sources
make verify         # fmt-check + lint + vet + test
make govulncheck    # vulnerability scan (requires govulncheck)
make build          # produces bin/mirador-rca
make image          # docker build tagged with git describe
make image-offline  # docker build with network access disabled
```

`make ci` runs the full verification plus `govulncheck` locally.

## Local Development Stack

Launch the Docker Compose sandbox (mock mirador-core, Valkey, Weaviate, and the service) with:

```
make localdev-up
```

Shut everything down and remove data volumes with `make localdev-down`. The stack definition lives under `deployment/localdev` and mounts your working copy so code changes are picked up instantly.

Run the service locally:
```
go run ./cmd/rca-engine --config configs/config.yaml
```

Build & publish a container image:
```
make docker-build IMAGE=ghcr.io/your-org/mirador-rca:$(git rev-parse --short HEAD)
make docker-push  IMAGE=ghcr.io/your-org/mirador-rca:$(git rev-parse --short HEAD)
```

Configuration fields are documented in `configs/config.example.yaml`.

## Valkey caching

Phase 4 adds read-through caching for Weaviate nearest-neighbour lookups and mirador-core service graph fetches. Configure the cache block in your config file (or via the `MIRADOR_RCA_CACHE_*` env vars) to point at a Valkey/Redis endpoint. Example:

```yaml
cache:
  addr: "valkey.mirador.svc.cluster.local:6379"
  username: ""
  db: 0
  tls: false
  similarIncidentsTTL: 2m
  serviceGraphTTL: 5m
```

If `addr` is blank the cache is disabled and requests fall back to direct Weaviate / mirador-core calls.

## LLM Integration with LMCache

mirador-rca supports optional LLM-powered analysis enhancement using vLLM with LMCache integration for heavy caching in airgapped environments with inference cards.

### Local Development

The localdev environment includes a vLLM service with LMCache enabled:

```bash
cd deployment/localdev
docker compose up --build
```

This starts vLLM with:
- Small model (`facebook/opt-125m`) for testing
- LMCache enabled with 2GB cache size
- Local CPU backend for development

### Production Deployment

Enable vLLM with LMCache in the Helm chart:

```bash
helm install mirador-rca charts/mirador-rca \
  --set vllm.enabled=true \
  --set vllm.model="microsoft/DialoGPT-medium" \
  --set vllm.resources.requests.nvidia\\.com/gpu=1
```

### Configuration

```yaml
vllm:
  enabled: true
  model: "microsoft/DialoGPT-medium"  # Override for production
  lmcache:
    enabled: true
    maxCacheSize: "10GB"  # Increase for production
    storageBackend: "LocalCPUBackend"  # Use GPU backend for inference cards
```

### Benefits

- **Heavy Caching**: LMCache provides efficient KV cache management for LLM inference
- **Airgapped Ready**: Optimized for environments without internet access
- **Inference Card Optimized**: Designed for GPU/accelerator-based inference
- **Fallback Support**: RCA works with or without LLM enhancement

## API

mirador-rca exposes both gRPC and REST APIs for root cause analysis operations.

### gRPC API

The primary API is gRPC, defined in `internal/grpc/proto/rca.proto`. The service runs on the port configured via `server.address` (defaults to `:50051`).

### REST API

A REST API equivalent is available on the port configured via `server.restAddress` (defaults to `:8080`). The REST API is fully compliant with OpenAPI 3.0.3 specifications.

**OpenAPI Specifications:**
- [YAML format](api/openapi.yaml)
- [JSON format](api/openapi.json)

**Endpoints:**
- `POST /api/v1/investigate` - Initiate root cause analysis
- `GET /api/v1/correlations` - List historical correlations
- `GET /api/v1/patterns` - Retrieve failure patterns
- `POST /api/v1/feedback` - Submit user feedback
- `GET /health` - Health check

## Metrics & Alerts

mirador-rca exposes Prometheus metrics on the HTTP endpoint configured via `server.metricsAddress` (defaults to `:2112`). The binary registers both the gRPC default metrics (`grpc_server_handled_total`, handling histograms) and custom RCA series:

- `mirador_rca_investigations_total{outcome="success|error"}`
- `mirador_rca_investigation_seconds`

Disable the endpoint by setting `server.metricsAddress: ""` (or `.Values.metrics.enabled=false` in the Helm chart). Refer to `docs/ops-observability.md` for the SLO catalogue, alert rules, and Grafana dashboard guidance.

## Helm deployment

A production-ready Helm chart lives under `charts/mirador-rca`. It ships with:

- Deployment + Service definitions with configurable probes and resources
- HorizontalPodAutoscaler targeting CPU and memory utilisation
- ConfigMap-driven application configuration
- Optional PrometheusRule alerts for investigation latency and traffic gaps
- A Grafana dashboard ConfigMap that visualises p95 latency and request volume

Render or install the chart locally:

```
helm lint charts/mirador-rca
helm install mirador-rca charts/mirador-rca \
  --set config.weaviate.endpoint=https://weaviate.example.com \
  --set runtimeSecrets.weaviateAPIKey.name=weaviate-credentials \
  --set runtimeSecrets.weaviateAPIKey.key=apiKey
```

## CI

GitHub Actions workflows in `.github/workflows` enforce linters, vet/test runs, Helm linting, and a scheduled `govulncheck` scan on pushes and pull requests to `main`.

## Release process

Follow `docs/release-process.md` for tagging, signing, and promoting releases. The document also references the SLO manifests under `deployment/infra/slo` and infrastructure-as-code assets used to stand up Valkey and Weaviate.
