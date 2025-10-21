SHELL := /bin/bash

ROOT := $(CURDIR)
GO ?= go
MODULE := github.com/miradorstack/mirador-rca
BINARY ?= mirador-rca
OUTPUT ?= $(ROOT)/bin
BUILD_ARTIFACT := $(OUTPUT)/$(BINARY)

GIT_DESCRIBE ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LD_FLAGS := -X $(MODULE)/internal/version.Commit=$(GIT_DESCRIBE)

GOFILES := $(shell find . -type f -name '*.go' -not -path './vendor/*' -not -path './.gocache/*' -not -path './.gotmp/*' -not -path './internal/grpc/generated/*')
IMAGE ?= ghcr.io/miradorstack/mirador-rca:$(GIT_DESCRIBE)
DOCKER ?= docker

GOIMPORTS ?= goimports
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK ?= govulncheck
HELM ?= helm

export GOCACHE ?= $(ROOT)/.gocache
export GOTMPDIR ?= $(ROOT)/.gotmp

COVER_PROFILE ?= coverage.out

.PHONY: help fmt fmt-check lint vet test test-cover build clean tidy vendor generate proto verify ci govulncheck docker-build image image-offline image-push helm-lint helm-template helm-package localdev-up localdev-down

help:
	@echo "Common targets:"
	@echo "  make fmt           - run gofmt and goimports"
	@echo "  make fmt-check     - verify formatting without modifying files"
	@echo "  make lint          - golangci-lint against ./..."
	@echo "  make test          - go test ./..."
	@echo "  make build         - build ./cmd/rca-engine"
	@echo "  make image         - docker build tagged with git describe"
	@echo "  make image-offline - docker build with network disabled"
	@echo "  make image-push    - push the built image"
	@echo "  make verify        - fmt-check + lint + vet + test"
	@echo "  make ci            - verify + govulncheck"
	@echo "  make tidy          - run go mod tidy"
	@echo "  make vendor        - vendor module dependencies"
	@echo "  make generate      - regenerate protobufs"
	@echo "  make govulncheck   - scan for known Go vulnerabilities"
	@echo "  make helm-lint     - lint Helm chart"
	@echo "  make helm-template - render Helm templates locally"
	@echo "  make helm-package  - package Helm chart into dist/"
	@echo "  make localdev-up   - start docker-compose sandbox"
	@echo "  make localdev-down - tear down docker-compose sandbox"
	@echo "  make clean         - remove build artifacts"

fmt:
	@echo "Formatting Go sources"
	@gofmt -w $(GOFILES)
	@command -v $(GOIMPORTS) >/dev/null 2>&1 || { echo "$(GOIMPORTS) not found; install via 'go install golang.org/x/tools/cmd/goimports@latest'"; exit 1; }
	@$(GOIMPORTS) -w $(GOFILES)

fmt-check:
	@command -v $(GOIMPORTS) >/dev/null 2>&1 || { echo "$(GOIMPORTS) not found; install via 'go install golang.org/x/tools/cmd/goimports@latest'"; exit 1; }
	@files=$$(gofmt -l $(GOFILES)); if [ -n "$$files" ]; then \
		echo "gofmt would reformat:"; echo "$$files"; exit 1; \
	fi
	@ imports=$$($(GOIMPORTS) -l $(GOFILES)); if [ -n "$$imports" ]; then \
		echo "$(GOIMPORTS) would reformat:"; echo "$$imports"; exit 1; \
	fi

lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "$(GOLANGCI_LINT) not found; install from https://golangci-lint.run"; exit 1; }
	@$(GOLANGCI_LINT) run ./...

vet:
	@$(GO) vet ./...

test:
	@$(GO) test ./...

test-cover:
	@$(GO) test ./... -coverprofile=$(COVER_PROFILE)

build:
	@mkdir -p $(OUTPUT)
	@$(GO) build -ldflags "$(LD_FLAGS)" -o $(BUILD_ARTIFACT) ./cmd/rca-engine

clean:
	@rm -rf $(OUTPUT) $(COVER_PROFILE) $(GOCACHE) $(GOTMPDIR)

tidy:
	@$(GO) mod tidy

vendor:
	@$(GO) mod vendor

proto: generate

generate:
	@./scripts/generate_proto.sh

verify: fmt-check lint vet test

ci: verify govulncheck

govulncheck:
	@command -v $(GOVULNCHECK) >/dev/null 2>&1 || { echo "$(GOVULNCHECK) not found; install via 'go install golang.org/x/vuln/cmd/govulncheck@latest'"; exit 1; }
	@mkdir -p $(GOTMPDIR)
	@$(GOVULNCHECK) ./...

image: docker-build

docker-build:
	@$(DOCKER) build --build-arg VERSION=$(GIT_DESCRIBE) -t $(IMAGE) .

image-offline:
	@$(DOCKER) build --network=none --build-arg VERSION=$(GIT_DESCRIBE) -t $(IMAGE) .

image-push:
	@if [ -z "$(IMAGE)" ]; then echo "IMAGE must be set"; exit 1; fi
	@$(DOCKER) push $(IMAGE)

helm-lint:
	@command -v $(HELM) >/dev/null 2>&1 || { echo "$(HELM) not found; install Helm"; exit 1; }
	@$(HELM) lint charts/mirador-rca

helm-template:
	@command -v $(HELM) >/dev/null 2>&1 || { echo "$(HELM) not found; install Helm"; exit 1; }
	@$(HELM) template mirador-rca charts/mirador-rca

helm-package:
	@command -v $(HELM) >/dev/null 2>&1 || { echo "$(HELM) not found; install Helm"; exit 1; }
	@mkdir -p $(ROOT)/dist
	@$(HELM) package charts/mirador-rca --destination $(ROOT)/dist

LOCALDEV_COMPOSE ?= docker compose -f $(ROOT)/deployment/localdev/docker-compose.yaml

localdev-up:
	@$(LOCALDEV_COMPOSE) up --build

localdev-down:
	@$(LOCALDEV_COMPOSE) down -v*** End of File
