.PHONY: generate fmt lint test build run-control run-gateway run-tenant docker-up docker-down smoke smoke-saas smoke-software

GO ?= go

generate:
	$(GO) generate ./...

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

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
	bash scripts/onboarding-smoke-test.sh
	bash scripts/documents-smoke-test.sh
	bash scripts/email-smoke-test.sh
	bash scripts/smoke-test.sh
	bash scripts/rag-smoke-test.sh

smoke-saas:
	bash scripts/saas-onboarding-smoke-test.sh

smoke-software:
	bash scripts/saas-onboarding-smoke-test.sh
	bash scripts/msp-onboarding-smoke-test.sh
