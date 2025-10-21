```bash
mirador-rca/
├── cmd/
│   └── rca-engine/
│       └── main.go              # entrypoint: wire config, deps, start gRPC server
│
├── internal/
│   ├── api/
│   │   ├── server.go            # gRPC server setup, middleware, health check
│   │   └── handlers.go          # maps gRPC requests → engine services
│   │
│   ├── grpc/
│   │   ├── proto/
│   │   │   └── rca.proto        # RCAEngine service & messages
│   │   ├── generated/           # compiled gRPC stubs (Go)
│   │   └── clients/             # if RCA calls other services (e.g., weaviate)
│   │
│   ├── config/
│   │   └── config.go            # struct definitions, env var/YAML parsing
│   │
│   ├── extractors/              # anomaly feature extraction per signal
│   │   ├── metrics.go
│   │   ├── logs.go
│   │   └── traces.go
│   │
│   ├── engine/                  # RCA core logic
│   │   ├── correlate.go         # candidate generation + ranking
│   │   ├── causality.go         # causality validation
│   │   ├── recommend.go         # rule-based recommendations
│   │   └── pipeline.go          # orchestrator pipeline
│   │
│   ├── patterns/                # failure pattern miner + store
│   │   ├── miner.go
│   │   └── repo.go              # persistence in Weaviate
│   │
│   ├── repo/                    # data access layer
│   │   ├── weaviate_repo.go     # read/write incidents, patterns
│   │   ├── victoria_metrics.go  # MetricsQL queries
│   │   ├── victoria_logs.go     # LogsQL queries
│   │   └── victoria_traces.go   # Traces queries
│   │
│   ├── models/                  # domain models (mirror core’s)
│   │   ├── correlation.go
│   │   ├── pattern.go
│   │   └── requests.go
│   │
│   ├── services/                # orchestrated business services
│   │   └── rca_service.go       # implements RCAEngine interface
│   │
│   └── utils/
│       ├── logger.go            # structured logging
│       ├── timeutils.go
│       └── errors.go
│
├── pkg/
│   └── cache/
│       └── valkey_cache.go      # optional cache/rate limit
│
├── api/
│   └── openapi.md               # optional doc: mirror proto → REST mapping
│
├── configs/
│   ├── config.yaml              # default config
│   └── config.example.yaml
│
├── charts/                      # Helm chart for deployment
│   └── mirador-rca/
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
│           ├── deployment.yaml
│           ├── service.yaml
│           ├── configmap.yaml
│           └── hpa.yaml
│
├── scripts/
│   ├── generate_proto.sh        # regenerate gRPC stubs
│   └── dev_run.sh               # local run helpers
│
├── test/
│   ├── integration/
│   │   └── investigate_test.go
│   └── e2e/
│       └── investigate_e2e_test.go
│
├── .gitignore
├── go.mod
├── go.sum
├── Dockerfile
├── Makefile
└── README.md
```