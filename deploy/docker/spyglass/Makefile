LOCAL_PROVIDER_ENV ?=
LOCAL_PROVIDER_ENV_ARG = $(if $(strip $(LOCAL_PROVIDER_ENV)),--env-file "$(LOCAL_PROVIDER_ENV)")
COMPOSE = docker compose --project-name spyglass-local --env-file env/local.env $(LOCAL_PROVIDER_ENV_ARG) --file compose.yml --file compose.local.yml
TLS_COMPOSE = docker compose --project-name spyglass-local-tls --env-file env/local.env $(LOCAL_PROVIDER_ENV_ARG) --env-file env/workload-tls.env --file compose.yml --file compose.local.yml --file compose.workload-tls.yml
RUNNER_COMPOSE = docker compose --project-name spyglass-local-runner --env-file env/local.env $(LOCAL_PROVIDER_ENV_ARG) --env-file env/workload-tls.env --file compose.yml --file compose.local.yml --file compose.workload-tls.yml --file compose.runner.yml

.PHONY: build test verify-local-provider-input up verify verify-commercial-journey analytics-report verify-analytics-handoff verify-identity-browser verify-public-catalog-recovery verify-stage-contract verify-stage-reset verify-stage-release-publisher publish-stage-release verify-observability verify-workload-tls verify-docker-runner verify-integration-connector verify-google-oauth verify-imap verify-web-research down down-workload-tls down-docker-runner reset

build:
	$(COMPOSE) build global-migrate website

test:
	bash test-local.sh

verify-local-provider-input:
	@if [ -z "$(strip $(LOCAL_PROVIDER_ENV))" ]; then exit 0; fi; \
	test -f "$(LOCAL_PROVIDER_ENV)" && test ! -L "$(LOCAL_PROVIDER_ENV)" || { echo "LOCAL_PROVIDER_ENV must be a regular non-symlink file" >&2; exit 1; }; \
	case "$$(readlink -f -- "$(LOCAL_PROVIDER_ENV)")" in /mnt/*) echo "LOCAL_PROVIDER_ENV must be on the native UbuntuRojo filesystem, not a Windows mount" >&2; exit 1;; esac; \
	test "$$(stat -c %a "$(LOCAL_PROVIDER_ENV)")" = 600 || { echo "LOCAL_PROVIDER_ENV must have mode 600" >&2; exit 1; }; \
	! grep -Eqi 'replace|not_a_real|example' "$(LOCAL_PROVIDER_ENV)" || { echo "LOCAL_PROVIDER_ENV contains placeholder values" >&2; exit 1; }; \
	grep -Eq '^SPYGLASS_STRIPE_SECRET_KEY=sk_test_[A-Za-z0-9_]+$$' "$(LOCAL_PROVIDER_ENV)" || { echo "LOCAL_PROVIDER_ENV requires a Stripe test secret key" >&2; exit 1; }; \
	grep -Eq '^SPYGLASS_STRIPE_WEBHOOK_SECRET=whsec_[A-Za-z0-9_]+$$' "$(LOCAL_PROVIDER_ENV)" || { echo "LOCAL_PROVIDER_ENV requires the endpoint-specific local webhook secret" >&2; exit 1; }; \
	grep -Eq '^SPYGLASS_STRIPE_MODE=test$$' "$(LOCAL_PROVIDER_ENV)" || { echo "LOCAL_PROVIDER_ENV must select Stripe test mode" >&2; exit 1; }; \
	! grep -Evq '^(#.*|[[:space:]]*|SPYGLASS_STRIPE_SECRET_KEY=sk_test_[A-Za-z0-9_]+|SPYGLASS_STRIPE_WEBHOOK_SECRET=whsec_[A-Za-z0-9_]+|SPYGLASS_STRIPE_MODE=test)$$' "$(LOCAL_PROVIDER_ENV)" || { echo "LOCAL_PROVIDER_ENV contains an unsupported key or malformed line" >&2; exit 1; }

up: verify-local-provider-input
	$(COMPOSE) up --detach --build --force-recreate --wait

verify: test up
	bash verify-local.sh
	bash verify-identity-browser.sh
	bash verify-analytics-handoff.sh
	bash verify-commercial-journey.sh

verify-commercial-journey:
	bash verify-commercial-journey.sh

analytics-report: up
	$(COMPOSE) run --rm --no-deps global-migrate analytics-report $(ARGS)

verify-analytics-handoff: up
	bash verify-analytics-handoff.sh

verify-identity-browser: up
	bash verify-identity-browser.sh

verify-public-catalog-recovery:
	bash verify-public-catalog-recovery.sh

verify-stage-contract:
	bash test-stage-contract.sh

verify-stage-reset:
	bash test-reset-stage-signup.sh

verify-stage-release-publisher:
	bash test-publish-stage-release.sh

publish-stage-release:
	bash publish-stage-release.sh "$(VERSION)"

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

verify-web-research:
	bash verify-web-research.sh

down:
	$(COMPOSE) down --remove-orphans

down-workload-tls:
	$(TLS_COMPOSE) down --remove-orphans

down-docker-runner:
	SPYGLASS_DOCKER_RUNNER_IDENTITY_DIRECTORY="$(CURDIR)/workload-tls/runner-identities" $(RUNNER_COMPOSE) down --remove-orphans

reset:
	@test "$(CONFIRM)" = "spyglass-local" || (echo "set CONFIRM=spyglass-local to remove local containers and volumes" >&2; exit 1)
	$(COMPOSE) down --volumes --remove-orphans
