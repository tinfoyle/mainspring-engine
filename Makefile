.PHONY: generate ui-install ui-build fmt lint test test-fast test-full test-baseline test-document-recall test-unknown-answer test-web-research build run-control run-gateway run-tenant docker-up docker-down smoke smoke-mcp smoke-agent smoke-agent-customization smoke-capacity smoke-codex smoke-work smoke-startup smoke-saas smoke-software

GO ?= go

generate:
	$(GO) generate ./...

ui-install:
	cd ui && npm ci

ui-build:
	cd ui && npm run build

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-fast:
	$(GO) test ./...
	$(GO) vet ./...

test-full:
	bash scripts/full-test-suite.sh

# Runs against an already-started development stack.
test-baseline:
	bash scripts/baseline-onboarding-smoke-test.sh

test-document-recall:
	bash scripts/document-recall-smoke-test.sh

test-unknown-answer:
	bash scripts/unknown-answer-smoke-test.sh

test-web-research:
	$(GO) test ./internal/webresearch ./internal/tools ./internal/tenant -run 'Web|MCPWeb'

build: generate
	$(GO) build -trimpath -o bin/mainspring ./cmd/mainspring

run-control:
	$(GO) run ./cmd/mainspring control

run-gateway:
	$(GO) run ./cmd/mainspring gateway

run-tenant:
	$(GO) run ./cmd/mainspring tenant

docker-up:
	docker compose -f deploy/docker/compose.dev.yml up --build

docker-down:
	docker compose -f deploy/docker/compose.dev.yml down

smoke:
	bash scripts/mcp-smoke-test.sh
	bash scripts/bootstrap-smoke-test.sh
	bash scripts/onboarding-smoke-test.sh
	bash scripts/unknown-answer-smoke-test.sh
	bash scripts/baseline-onboarding-smoke-test.sh
	bash scripts/documents-smoke-test.sh
	bash scripts/document-recall-smoke-test.sh
	bash scripts/agent-customization-smoke-test.sh
	bash scripts/agent-platform-smoke-test.sh
	bash scripts/startup-onboarding-smoke-test.sh
	bash scripts/email-smoke-test.sh
	bash scripts/workqueue-smoke-test.sh
	bash scripts/smoke-test.sh
	bash scripts/rag-smoke-test.sh

smoke-mcp:
	bash scripts/mcp-smoke-test.sh

smoke-agent:
	bash scripts/agent-platform-smoke-test.sh

smoke-agent-customization:
	bash scripts/agent-customization-smoke-test.sh

smoke-capacity:
	bash scripts/capacity-smoke-test.sh

# Requires a worker started with MAINSPRING_RUNNER_PROVIDER=codex and a
# single-turn test boardroom. It incurs a real provider invocation.
smoke-codex:
	bash scripts/codex-runner-smoke-test.sh

smoke-work:
	bash scripts/workqueue-smoke-test.sh

smoke-startup:
	bash scripts/startup-onboarding-smoke-test.sh

smoke-saas:
	bash scripts/saas-onboarding-smoke-test.sh

smoke-software:
	bash scripts/saas-onboarding-smoke-test.sh
	bash scripts/msp-onboarding-smoke-test.sh
