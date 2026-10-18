# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
# SPDX-License-Identifier: AGPL-3.0-only
#
# Folio image: the folio binary plus the pinned, checksum-verified official
# static pandoc and typst builds on Debian slim. Fonts, base layouts and
# vendored Typst packages are embedded in the folio binary.
#
#   docker build -t folio .
#   docker buildx build --platform linux/amd64,linux/arm64 -t folio .

# Base images are pinned by digest in the FROM lines (Dependabot updates them);
# the BuildKit frontend is pinned by digest in the syntax line.

# ---- fetch: pinned pandoc and typst for the target architecture ----------
FROM --platform=$BUILDPLATFORM debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f AS fetch
ARG TARGETARCH
ARG PANDOC_VERSION=3.12.1
ARG PANDOC_SHA256_AMD64=d0c90410e90204c9ca83b8539fac5c7aed01fd537207e4585849f8abc5df20b8
ARG PANDOC_SHA256_ARM64=445d96fd08801fe636cab1158b4f017ee320ac3b7446328b4fe2f902117b19b9
ARG TYPST_VERSION=0.15.1
ARG TYPST_SHA256_AMD64=a6d077d0a95eed5a2eba715b2dae06be954f624ccbf85758a03f389ded33118c
ARG TYPST_SHA256_ARM64=5aa8d74a3d906e60ea12a66ac2f37f8eef1b14cbad7182a745e393a10c23dcee
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl xz-utils \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /fetch
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) parch=amd64; tarch=x86_64;  psum="$PANDOC_SHA256_AMD64"; tsum="$TYPST_SHA256_AMD64" ;; \
      arm64) parch=arm64; tarch=aarch64; psum="$PANDOC_SHA256_ARM64"; tsum="$TYPST_SHA256_ARM64" ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o pandoc.tar.gz \
      "https://github.com/jgm/pandoc/releases/download/${PANDOC_VERSION}/pandoc-${PANDOC_VERSION}-linux-${parch}.tar.gz"; \
    echo "${psum}  pandoc.tar.gz" | sha256sum -c -; \
    tar -xzf pandoc.tar.gz --strip-components=2 "pandoc-${PANDOC_VERSION}/bin/pandoc"; \
    curl -fsSL -o typst.tar.xz \
      "https://github.com/typst/typst/releases/download/v${TYPST_VERSION}/typst-${tarch}-unknown-linux-musl.tar.xz"; \
    echo "${tsum}  typst.tar.xz" | sha256sum -c -; \
    tar -xJf typst.tar.xz --strip-components=1 "typst-${tarch}-unknown-linux-musl/typst"; \
    chmod 0755 pandoc typst; \
    rm pandoc.tar.gz typst.tar.xz

# ---- build: folio, cross-compiled on the build platform ------------------
FROM --platform=$BUILDPLATFORM golang:1.27.2-trixie@sha256:47667934874913a449b96faa7ef4777fa285d9ec29126ee80ed61c45b7cccf0e AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=devel
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.buildVersion=${VERSION}" -o /out/folio ./cmd/folio

# ---- runtime ---------------------------------------------------------------
FROM debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f
ARG VERSION=devel
LABEL org.opencontainers.image.title="folio" \
      org.opencontainers.image.description="Branded PDF and DOCX documents for AI agents over MCP" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.version="${VERSION}"
# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 47100 folio \
 && useradd --system --uid 47100 --gid folio --no-create-home --home-dir /nonexistent \
      --shell /usr/sbin/nologin folio \
 && mkdir -p /data \
 && chown folio:folio /data
COPY --from=fetch --chown=root:root /fetch/pandoc /fetch/typst /usr/local/bin/
COPY --from=build --chown=root:root /out/folio /usr/local/bin/folio
COPY --chmod=0755 docker/entrypoint.sh /usr/local/bin/folio-entrypoint
ENV FOLIO_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER 47100:47100
HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 CMD ["folio", "healthcheck"]
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/folio-entrypoint"]
CMD ["serve"]
