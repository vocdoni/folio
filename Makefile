# SPDX-License-Identifier: AGPL-3.0-only
#
# Developer shortcuts. Rendering tests need pandoc 3.12.1 and typst 0.15.1 on
# PATH or in FOLIO_PANDOC / FOLIO_TYPST.

GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
IMAGE   ?= folio:dev
LDFLAGS := -s -w -X main.buildVersion=$(VERSION)

.PHONY: build test test-race lint generate docker docker-smoke run clean

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

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

docker-smoke: docker
	scripts/docker-smoke.sh $(IMAGE)

# A local instance with a throwaway token printed at startup and data in ./data.
run: build
	@token="$$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"; \
	echo "FOLIO_TOKEN=$$token"; \
	FOLIO_PUBLIC_URL=http://localhost:8080 FOLIO_DATA_DIR=./data FOLIO_LOG_FORMAT=text \
	FOLIO_TOKEN="$$token" ./bin/folio serve

clean:
	rm -rf bin
