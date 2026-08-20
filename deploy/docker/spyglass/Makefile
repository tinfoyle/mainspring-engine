COMPOSE = docker compose --project-name spyglass-local --file compose.yml --file compose.local.yml

.PHONY: build test up verify down reset

build:
	$(COMPOSE) build application website

test:
	$(COMPOSE) --profile test build website-test

up:
	$(COMPOSE) up --detach --build --wait

verify: test up
	bash verify-local.sh

down:
	$(COMPOSE) down --remove-orphans

reset:
	@test "$(CONFIRM)" = "spyglass-local" || (echo "set CONFIRM=spyglass-local to remove local containers and volumes" >&2; exit 1)
	$(COMPOSE) down --volumes --remove-orphans
