COMPOSE = docker compose --project-name spyglass-local --env-file env/local.env --file compose.yml --file compose.local.yml
TLS_COMPOSE = docker compose --project-name spyglass-local-tls --env-file env/local.env --env-file env/workload-tls.env --file compose.yml --file compose.local.yml --file compose.workload-tls.yml
RUNNER_COMPOSE = docker compose --project-name spyglass-local-runner --env-file env/local.env --env-file env/workload-tls.env --file compose.yml --file compose.local.yml --file compose.workload-tls.yml --file compose.runner.yml

.PHONY: build test up verify verify-stage-contract verify-observability verify-workload-tls verify-docker-runner verify-integration-connector verify-google-oauth verify-imap down down-workload-tls down-docker-runner reset

build:
	$(COMPOSE) build global-migrate website

test:
	bash test-local.sh

up:
	$(COMPOSE) up --detach --build --force-recreate --wait

verify: test up
	bash verify-local.sh

verify-stage-contract:
	bash test-stage-contract.sh

verify-observability: up
	bash verify-observability.sh

verify-workload-tls:
	bash verify-workload-tls.sh

verify-docker-runner:
	bash verify-docker-runner.sh

verify-integration-connector:
	bash verify-integration-connector.sh

verify-google-oauth:
	bash verify-google-oauth.sh

verify-imap:
	bash verify-imap.sh

down:
	$(COMPOSE) down --remove-orphans

down-workload-tls:
	$(TLS_COMPOSE) down --remove-orphans

down-docker-runner:
	SPYGLASS_DOCKER_RUNNER_IDENTITY_DIRECTORY="$(CURDIR)/workload-tls/runner-identities" $(RUNNER_COMPOSE) down --remove-orphans

reset:
	@test "$(CONFIRM)" = "spyglass-local" || (echo "set CONFIRM=spyglass-local to remove local containers and volumes" >&2; exit 1)
	$(COMPOSE) down --volumes --remove-orphans
