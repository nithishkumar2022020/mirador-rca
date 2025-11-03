# Local Development Environment

This Docker Compose stack spins up everything required to exercise `mirador-rca` on a laptop:

- `core-mock`: a lightweight HTTP stub that mimics the mirador-core RCA endpoints.
- `valkey`: in-memory cache compatible with the service configuration.
- `weaviate`: external similarity store (anonymous access enabled).
- `vllm-lmcache`: vLLM server with LMCache enabled for LLM-powered RCA analysis.
- `mirador-rca`: the service itself, executed via `go run` against the workspace tree.

## Prerequisites
- Docker Desktop or Docker Engine 24+
- Docker Compose plugin (v2+)

## Usage

From the repository root:

```bash
cd deployment/localdev
docker compose up --build
```

The first boot downloads Go modules; subsequent runs reuse the module cache within the container layer.

### Service Endpoints
- gRPC: `localhost:50051`
- Prometheus metrics: `http://localhost:2112/metrics`
- Mock mirador-core HTTP APIs: `http://localhost:8080`
- Weaviate console/API: `http://localhost:8081`
- Valkey: `localhost:6379`
- vLLM with LMCache: `http://localhost:8000` (OpenAI-compatible API)

### Example gRPC Invocation

With `grpcurl` installed:

```bash
grpcurl -plaintext -d '{
  "incidentId": "INC-123",
  "tenantId": "tenant-a",
  "symptoms": ["checkout latency"],
  "timeRange": {
    "start": "2024-09-22T11:00:00Z",
    "end": "2024-09-22T11:10:00Z"
  }
}' localhost:50051 mirador.rca.v1.RCAEngine/InvestigateIncident
```

The mock core returns deterministic data, so you should receive a correlation response populated with anchors, timeline, and recommendations.

## LLM-Powered RCA Analysis

When enabled, `mirador-rca` integrates with vLLM + LMCache for enhanced root cause analysis:

- **LMCache**: Provides heavy caching for LLM KV caches, optimized for inference cards in airgapped environments
- **LLM Analysis**: Generates additional insights and recommendations using the configured language model
- **Fallback Support**: RCA works normally even if LLM service is unavailable

The vLLM service is configured with:
- Small model (`facebook/opt-125m`) for fast local testing
- LMCache enabled with 2GB cache size
- Local CPU backend for development
- LRU eviction policy

To test LLM features, ensure the vLLM service is healthy before running investigations.

## Tear Down & Data Reset

```bash
cd deployment/localdev
docker compose down -v
```

This removes the containers and the persisted Weaviate volume (`weaviate-data`).

## Customising the Stack
- Edit `config/mirador-rca.yaml` to point at alternative services or tweak cache TTLs.
- Modify `mock-core/main.go` to extend the simulated payloads.
- Add extra services (e.g. Jaeger, Prometheus) by extending `docker-compose.yaml`.

## Common Issues
- **Port conflicts**: ensure nothing else listens on 50051/8080/8081/2112/6379.
- **Module downloads**: the initial `go run` fetches dependencies from the internet; if you are offline, run `go mod download` on the host first so the module cache is available inside the bind mount.
- **Weaviate startup time**: the service may take a few seconds before accepting traffic. `mirador-rca` will retry its first calls; restart the container if you hit transient errors.
