#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Runs the web UI end-to-end tests (web/e2e, Playwright) against a Folio
# container whose OpenRouter base URL points at a stub OpenRouter container
# (web/e2e/stub-openrouter.mjs) on a private Docker network. No real model
# is ever called.
#
#   scripts/ui-e2e.sh <image>      (needs: docker, curl, Node 24 with web/node_modules)
set -euo pipefail

image="${1:?usage: scripts/ui-e2e.sh <image>}"
root="$(cd "$(dirname "$0")/.." && pwd)"
name="folio-ui-e2e-$$"
port="${UI_E2E_PORT:-18090}"
url="http://127.0.0.1:$port"
password="$(openssl rand -hex 16)" # random per run, never printed
token="$(openssl rand -hex 24)"
node_image="node:24-trixie-slim@sha256:7f486cabe892bff8fef04c0f7da8dba84a560be3e4e2740b7a86afc57ebec315"

# shellcheck disable=SC2329 # invoked by the EXIT trap
cleanup() {
	docker rm -f "$name-folio" "$name-stub" >/dev/null 2>&1 || true
	docker network rm "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker network create "$name" >/dev/null
docker run -d --name "$name-stub" --network "$name" --network-alias stub \
	-v "$root/web/e2e:/e2e:ro" "$node_image" node /e2e/stub-openrouter.mjs 8181 >/dev/null
docker run -d --name "$name-folio" --network "$name" -p "127.0.0.1:$port:8080" \
	-e FOLIO_PUBLIC_URL="$url" -e FOLIO_TOKENS="agent:$token" -e FOLIO_UI_PASSWORD="$password" \
	-e FOLIO_OPENROUTER_API_KEY="sk-or-e2e-stub-not-a-real-key" -e FOLIO_OPENROUTER_BASE_URL="http://stub:8181/api/v1" \
	"$image" >/dev/null
for _ in $(seq 1 120); do
	if curl -fsS "$url/healthz" >/dev/null 2>&1; then break; fi
	sleep 1
done
curl -fsS "$url/healthz" >/dev/null || { docker logs "$name-folio" 2>&1 | tail -40; exit 1; }

cd "$root/web"
status=0
FOLIO_URL="$url" FOLIO_UI_PASSWORD="$password" FOLIO_E2E_TOKEN="$token" npx playwright test || status=$?
if [ "$status" -ne 0 ]; then
	docker logs "$name-folio" 2>&1 | tail -60
	docker logs "$name-stub" 2>&1 | tail -20
fi
exit "$status"
