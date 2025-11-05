## LLM Integration (vLLM + LMCache)

This guide explains how to enable and operate the optional LLM integration for mirador-rca using vLLM with LMCache. The LLM provides natural-language summaries and additional insights; the RCA pipeline continues to function without it.

### Configuration

Add the following block to your `configs/config.yaml`:

```yaml
llm:
  enabled: true
  baseURL: "http://vllm:8000"
  timeout: 30s
```

Environment overrides:

```bash
export MIRADOR_RCA_LLM_ENABLED=true
export MIRADOR_RCA_LLM_BASE_URL=http://vllm:8000
export MIRADOR_RCA_LLM_TIMEOUT=30s
```

### Local Development

Use the local dev stack which includes a small vLLM model with LMCache enabled:

```bash
cd deployment/localdev
docker compose up -d
```

### Helm Deployment

Set these Helm values to enable vLLM in-cluster:

```yaml
config:
  llm:
    enabled: true
    baseURL: "http://{{ include \"mirador-rca.fullname\" . }}-vllm:8000"
    timeout: 30s
vllm:
  enabled: true
  model: "facebook/opt-125m"
  lmcache:
    enabled: true
    maxCacheSize: "2GB"
```

### Runtime Behavior

- Circuit breaker: opens after a few consecutive failures and auto-resets.
- Client-side TTL cache: memoizes recent prompt responses to reduce latency.
- Metrics: `mirador_llm_requests_total{outcome}`, `mirador_llm_request_duration_seconds`.

### Airgapped Considerations

- Preload vLLM container images and models into your private registry.
- Disable any internet egress from pods; the integration uses only local network endpoints.

### Troubleshooting

- Check service health: `GET /health` and `GET /metrics`.
- Look for circuit breaker warnings in logs; ensure `baseURL` is reachable.

