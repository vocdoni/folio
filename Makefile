# SPDX-License-Identifier: AGPL-3.0-only
#
# Developer shortcuts. Rendering tests need pandoc 3.12.1 and typst 0.15.1 on
# PATH or in FOLIO_PANDOC / FOLIO_TYPST.

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
IMAGE   ?= folio:dev
LDFLAGS := -s -w -X main.buildVersion=$(VERSION)

.PHONY: build test test-race lint generate ui ui-check dev-ui docker docker-smoke ai-smoke run clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/folio ./cmd/folio

test:
	CGO_ENABLED=0 $(GO) test -count=1 ./...

test-race:
	CGO_ENABLED=1 $(GO) test -race -count=1 ./...

lint:
	golangci-lint run ./...

generate:
	$(GO) generate ./...

# The web UI (Node 24, see web/.nvmrc): builds into internal/webui/dist, which
# the next `make build` embeds. Without it /ui answers "UI assets not built".
ui:
	cd web && npm ci --no-audit --no-fund && npm run build

ui-check:
	cd web && npm run check && npm test

# Vite dev server on :5173 proxying /ui/api to a local Folio started with
# `make run` and FOLIO_UI_PASSWORD (FOLIO_URL overrides the target).
dev-ui:
	cd web && FOLIO_URL=$${FOLIO_URL:-http://localhost:8080} npm run dev

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

docker-smoke: docker
	scripts/docker-smoke.sh $(IMAGE)

# Manual, not CI: runs the web assistants against the real OpenRouter model.
# FOLIO_OPENROUTER_API_KEY=... make ai-smoke [URL=https://example.com]
ai-smoke:
	scripts/ai-smoke.sh $(URL)

# A local instance with a throwaway token printed at startup and data in ./data.
run: build
	@token="$$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"; \
	echo "FOLIO_TOKEN=$$token"; \
	FOLIO_PUBLIC_URL=http://localhost:8080 FOLIO_DATA_DIR=./data FOLIO_LOG_FORMAT=text \
	FOLIO_TOKEN="$$token" FOLIO_UI_PASSWORD="$${FOLIO_UI_PASSWORD:-}" ./bin/folio serve

clean:
	rm -rf bin web/node_modules web/test-results web/playwright-report
	find internal/webui/dist -mindepth 1 ! -name .keep -delete
