# Everything runs in a container, so a fresh clone works on a machine with no Go and no
# PostgreSQL: that is the difference between a repository and a screenshot.
IMAGE ?= golang:1.25
CACHE ?= liveops-gocache
RUN   := docker run --rm -v $(PWD):/app -v $(CACHE):/go -w /app $(IMAGE)

API_PORT     ?= 18100
PANEL_PORT   ?= 18101
export LIVEOPS_API_PORT := $(API_PORT)
export LIVEOPS_PANEL_PORT := $(PANEL_PORT)
TEST_DB      ?= liveops-test-db
TEST_PORT    ?= 55432
TEST_DSN     := postgres://liveops:liveops@127.0.0.1:$(TEST_PORT)/liveops?sslmode=disable

.PHONY: help demo test test-db coverage lint build image up down logs psql chart diagram

help:
	@echo "make demo       compose up from empty, the story, the worker twice, the counters"
	@echo "make worker     one worker pass only (run it twice by hand to see zero)"
	@echo "make panel      the panel container, against a running stack (needs make up)"
	@echo "make panel-dev  vite dev server with hot reload, proxying to :$(API_PORT)"
	@echo "make test       go test -race ./...                (unit tests only)"
	@echo "make test-db    start PostgreSQL, run every test including the integration ones"
	@echo "make coverage   go test -race -coverprofile, then the total"
	@echo "make lint       gofmt + go vet"
	@echo "make image      docker build, then migrate and work against a real database"
	@echo "make up / down  docker compose up -d --build / down -v"
	@echo "make logs       docker compose logs -f"
	@echo "make psql       psql inside the compose database"
	@echo "make chart      helm lint --strict and render both branches"
	@echo "make diagram    check docs/figures: committed, exported, not older than its sources"

# --- the demo: the order matters -------------------------------------------------
#
# Three properties are on display and each one needs the right order:
#   * the database starts empty, so the version numbers in the output are a fact rather
#     than a leftover (an earlier version of this target printed v5 because a previous
#     run had not been cleaned up);
#   * the worker runs twice, because a claim that happens once is only visible if
#     something tried to make it happen twice;
#   * the report runs last, so the counters it prints are the result of both passes.
#
# The demo client and the worker are one-off containers on the compose network: same
# image, different entrypoint, no third service for `compose up` to start by accident.
NET    := liveops-console_default
DSN    := postgres://liveops:liveops@postgres:5432/liveops?sslmode=disable
IMG    := liveops-console:local

demo:
	@docker compose down -v >/dev/null 2>&1 || true
	@docker compose up -d --build >/dev/null
	@echo "waiting for the api on :$(API_PORT)"
	@for i in $$(seq 1 40); do curl -fsS http://localhost:$(API_PORT)/healthz >/dev/null 2>&1 && break; sleep 2; done
	@docker run --rm --network $(NET) -e LIVEOPS_API=http://api:8080 --entrypoint /usr/local/bin/demo $(IMG)
	@echo
	@docker run --rm --network $(NET) -e LIVEOPS_DSN='$(DSN)' --entrypoint /usr/local/bin/console $(IMG) --mode worker --once
	@docker run --rm --network $(NET) -e LIVEOPS_DSN='$(DSN)' --entrypoint /usr/local/bin/console $(IMG) --mode worker --once
	@echo
	@docker run --rm --network $(NET) -e LIVEOPS_API=http://api:8080 --entrypoint /usr/local/bin/demo $(IMG) --report
	@echo
	@echo "panel: http://localhost:$(PANEL_PORT)  (paste admin-token, op-token or view-token)"

# `make worker` is what the demo uses twice, available on its own because the property
# it demonstrates is worth poking at: run it three times and watch the third say zero.
worker:
	@docker run --rm --network $(NET) -e LIVEOPS_DSN='$(DSN)' --entrypoint /usr/local/bin/console $(IMG) --mode worker --once

# --- tests -----------------------------------------------------------------------
test:
	@$(RUN) go test -race ./...

# The test database runs in a container and the tests join its network namespace, so
# the DSN is localhost from inside the container and no host port mapping is assumed.
test-db:
	@docker rm -f $(TEST_DB) >/dev/null 2>&1 || true
	@docker run -d --rm --name $(TEST_DB) -e POSTGRES_PASSWORD=liveops -e POSTGRES_USER=liveops -e POSTGRES_DB=liveops postgres:16-alpine >/dev/null
	@echo "waiting for $(TEST_DB)"
	@for i in $$(seq 1 40); do docker exec $(TEST_DB) pg_isready -U liveops >/dev/null 2>&1 && break; sleep 0.5; done
	@docker run --rm --network container:$(TEST_DB) -v $(PWD):/app -v $(CACHE):/go -w /app \
		-e LIVEOPS_TEST_DSN='postgres://liveops:liveops@127.0.0.1:5432/liveops?sslmode=disable' \
		$(IMAGE) go test -race -coverprofile=/tmp/cover.out -covermode=atomic ./... || (docker stop $(TEST_DB) >/dev/null; exit 1)
	@docker run --rm --network container:$(TEST_DB) -v $(PWD):/app -v $(CACHE):/go -w /app \
		-e LIVEOPS_TEST_DSN='postgres://liveops:liveops@127.0.0.1:5432/liveops?sslmode=disable' \
		$(IMAGE) sh -c 'go tool cover -func=/tmp/cover.out | grep -E "^(total|github.com/lasttoss/liveops-console/internal/pgstore)"'
	@docker stop $(TEST_DB) >/dev/null
	@echo "test database stopped"

coverage:
	@$(RUN) sh -c 'go test -race -coverprofile=/tmp/cover.out -covermode=atomic ./... >/dev/null && go tool cover -func=/tmp/cover.out | tail -1'

lint:
	@$(RUN) sh -c 'test -z "$$(gofmt -l .)" && echo "gofmt clean" ; go vet ./... && echo "vet clean"'

build:
	@$(RUN) go build -o /tmp/console ./cmd/console && echo "console builds"

image:
	@docker build -q -t liveops-console:local . >/dev/null
	@echo "image ok: $$(docker run --rm liveops-console:local --mode migrate 2>&1 | head -1)"

panel:
	@docker compose up -d --build panel
	@for i in $$(seq 1 30); do curl -fsS http://localhost:$(PANEL_PORT)/healthz >/dev/null 2>&1 && break; sleep 1; done
	@echo "panel: http://localhost:$(PANEL_PORT)"

panel-dev:
	@cd panel && npm install --no-audit --no-fund >/dev/null && npm run dev

up:
	@docker compose up -d --build

down:
	@docker compose down -v

logs:
	@docker compose logs -f --tail=50

psql:
	@docker compose exec postgres psql -U liveops -d liveops

# --- the standard gates ----------------------------------------------------------
CHARTDIR := charts/liveops-console
HELM  := docker run --rm -v $(PWD):/apps alpine/helm:3.16.3
KCONF := docker run --rm -i ghcr.io/yannh/kubeconform:latest -strict -summary -kubernetes-version 1.30.0 -

# lint alone renders the defaults, and a branch that is not the default is where the
# broken template hides - so both branches are rendered and both are validated. Each
# render is also counted: kubeconform happily reports "Valid: 0" over an empty document,
# which is a green gate over nothing, and that is worse than no gate at all.
chart:
	@$(HELM) lint --strict /apps/$(CHARTDIR)
	@$(HELM) template liveops-console /apps/$(CHARTDIR) > /tmp/liveops-default.yaml
	@test $$(grep -c '^kind:' /tmp/liveops-default.yaml) -ge 5 || { echo "default branch rendered too few resources:"; grep -c '^kind:' /tmp/liveops-default.yaml; exit 1; }
	@$(KCONF) < /tmp/liveops-default.yaml
	@$(HELM) template liveops-console /apps/$(CHARTDIR) \
		--set auth.tokens='admin-token:em:admin\,op-token:ci:operator' \
		--set database.dsn='postgres://liveops:liveops@pg:5432/liveops?sslmode=disable' \
		--set autoscaling.enabled=true \
		--set networkPolicy.kubeletCIDR=10.0.0.0/8 \
		--set networkPolicy.ingress.metrics=false \
		--set worker.enabled=false > /tmp/liveops-full.yaml
	@test $$(grep -c '^kind:' /tmp/liveops-full.yaml) -ge 6 || { echo "full branch rendered too few resources"; exit 1; }
	@$(KCONF) < /tmp/liveops-full.yaml
	@echo "chart ok: both branches rendered, counted and validated"

diagram:
	@bash ~/.pi/agent/scripts/diagram-freshness.sh
