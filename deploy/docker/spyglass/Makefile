COMPOSE = docker compose --project-name spyglass-local --env-file env/local.env --file compose.yml --file compose.local.yml
TLS_COMPOSE = docker compose --project-name spyglass-local-tls --env-file env/local.env --env-file env/workload-tls.env --file compose.yml --file compose.local.yml --file compose.workload-tls.yml

.PHONY: build test up verify verify-observability verify-workload-tls down down-workload-tls reset

build:
	$(COMPOSE) build global-migrate website

test:
	bash test-local.sh

up:
	$(COMPOSE) up --detach --build --wait

verify: test up
	bash verify-local.sh

verify-observability: up
	bash verify-observability.sh

verify-workload-tls:
	bash verify-workload-tls.sh

down:
	$(COMPOSE) down --remove-orphans

down-workload-tls:
	$(TLS_COMPOSE) down --remove-orphans

reset:
	@test "$(CONFIRM)" = "spyglass-local" || (echo "set CONFIRM=spyglass-local to remove local containers and volumes" >&2; exit 1)
	$(COMPOSE) down --volumes --remove-orphans
