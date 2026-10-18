#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Smoke test for a built Folio image (used by CI and `make docker-smoke`):
#   scripts/docker-smoke.sh <image>
#
# A. Default user, named volume, only FOLIO_PUBLIC_URL and FOLIO_TOKEN set:
#    /healthz ok, 401 without or with a wrong token, MCP initialize,
#    seeded templates, render of the starter proposal, PDF and DOCX
#    downloads through /f/, the image health check, clean SIGTERM shutdown.
# B. Railway mode: root (--user 0) with a root-owned bind mount at /data that
#    contains a planted symlink to /etc/passwd: the database ends up owned by
#    folio, folio runs as uid 47100 without capabilities and with
#    no_new_privs, and the symlink target is untouched.
# C. Default user with a root-owned bind mount: exits 1 with a clear message.
#
# Needs docker, curl and jq. Temporary files go under $SMOKE_TMP (default
# $RUNNER_TEMP, else ~/.cache/folio/tmp), never /tmp.
set -euo pipefail

image="${1:?usage: scripts/docker-smoke.sh <image>}"
base="${SMOKE_TMP:-${RUNNER_TEMP:-$HOME/.cache/folio/tmp}}"
mkdir -p "$base"
work="$(mktemp -d "$base/folio-smoke.XXXXXX")"
name="folio-smoke-$$"
port_a="${SMOKE_PORT_A:-18091}"
port_b="${SMOKE_PORT_B:-18092}"
token="$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"

as_root() { # run a shell snippet as root in the image with $work at /w
	docker run --rm --user 0 --entrypoint /bin/sh -v "$work:/w" "$image" -c "$1"
}

cleanup() {
	docker rm -f "$name-a" "$name-b" >/dev/null 2>&1 || true
	docker volume rm -f "$name-vol" >/dev/null 2>&1 || true
	as_root 'rm -rf /w/data /w/rootonly' >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	for c in "$name-a" "$name-b"; do
		docker logs "$c" 2>&1 | tail -n 40 >&2 || true
	done
	exit 1
}

wait_healthy() { # url
	local _
	for _ in $(seq 1 90); do
		if curl -fsS "$1/healthz" 2>/dev/null; then
			return 0
		fi
		sleep 2
	done
	return 1
}

mcp() { # url token method params-json → JSON-RPC response
	local resp
	resp="$(curl -sS -X POST "$1/mcp" \
		-H "Authorization: Bearer $2" \
		-H 'Content-Type: application/json' \
		-H 'Accept: application/json, text/event-stream' \
		-d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$3\",\"params\":$4}")"
	if grep -q '^data: ' <<<"$resp"; then
		sed -n 's/^data: //p' <<<"$resp" | tail -n 1
	else
		printf '%s\n' "$resp"
	fi
}

status() { # method url [auth header]
	curl -s -o /dev/null -w '%{http_code}' -X "$1" "${@:3}" "$2"
}

echo "== A: default user, named volume, FOLIO_PUBLIC_URL + FOLIO_TOKEN only"
url="http://127.0.0.1:$port_a"
docker run -d --name "$name-a" -p "127.0.0.1:$port_a:8080" \
	-e FOLIO_PUBLIC_URL="$url" -e FOLIO_TOKEN="$token" \
	-v "$name-vol:/data" "$image" >/dev/null
health="$(wait_healthy "$url")" || fail "A: /healthz never answered"
jq -e '.status == "ok" and (.version | length > 0) and (.sandbox | length > 0)' <<<"$health" >/dev/null ||
	fail "A: unexpected /healthz body: $health"
echo "healthz: $health"
[ "$(status POST "$url/mcp")" = 401 ] || fail "A: /mcp without a token must be 401"
[ "$(status POST "$url/mcp" -H "Authorization: Bearer wrong-token-0123456789abcdefghijklmnop")" = 401 ] ||
	fail "A: /mcp with a wrong token must be 401"
init="$(mcp "$url" "$token" initialize '{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}')"
jq -e '.result.serverInfo.name == "folio" and (.result.instructions | length > 0)' <<<"$init" >/dev/null ||
	fail "A: initialize: $init"
list="$(mcp "$url" "$token" tools/call '{"name":"list_templates","arguments":{}}')"
jq -e '[.result.structuredContent.templates[].id] | sort == ["proposal","quote","requirements","sow","technical-report"]' <<<"$list" >/dev/null ||
	fail "A: seeded templates: $list"
tpl="$(mcp "$url" "$token" tools/call '{"name":"get_template","arguments":{"id":"proposal"}}')"
args="$(jq -c '.result.structuredContent.template.sample + {template_id: "proposal", include_previews: "none", idempotency_key: "docker-smoke"}' <<<"$tpl")"
res="$(mcp "$url" "$token" tools/call "{\"name\":\"render_document\",\"arguments\":$args}")"
jq -e '.result.isError != true and (.result.structuredContent.files | length == 2)' <<<"$res" >/dev/null ||
	fail "A: render_document: $(head -c 2000 <<<"$res")"
pdf_url="$(jq -r '.result.structuredContent.files[] | select(.kind == "pdf") | .url' <<<"$res")"
docx_url="$(jq -r '.result.structuredContent.files[] | select(.kind == "docx") | .url' <<<"$res")"
curl -fsS -o "$work/out.pdf" "$pdf_url" || fail "A: PDF download"
curl -fsS -o "$work/out.docx" "$docx_url" || fail "A: DOCX download"
[ "$(head -c 5 "$work/out.pdf")" = "%PDF-" ] || fail "A: the download is not a PDF"
[ "$(head -c 2 "$work/out.docx")" = "PK" ] || fail "A: the download is not a DOCX"
echo "rendered proposal: document $(jq -r '.result.structuredContent.document_id' <<<"$res") revision $(jq -r '.result.structuredContent.revision' <<<"$res")"
docker exec "$name-a" folio healthcheck || fail "A: folio healthcheck inside the container"
docker stop -t 90 "$name-a" >/dev/null
[ "$(docker inspect -f '{{.State.ExitCode}}' "$name-a")" = 0 ] || fail "A: non-zero exit after SIGTERM"
docker logs "$name-a" 2>&1 | grep -q 'shut down cleanly' || fail "A: no clean shutdown log line"

echo "== B: --user 0 with a root-owned bind mount (Railway mode)"
as_root 'mkdir -m 0755 /w/data && ln -s /etc/passwd /w/data/planted-link && chown -h 0:0 /w/data /w/data/planted-link'
[ "$(stat -c %u "$work/data")" = 0 ] || fail "B: the bind mount must start root-owned"
url="http://127.0.0.1:$port_b"
docker run -d --name "$name-b" --user 0 -p "127.0.0.1:$port_b:8080" \
	-e FOLIO_PUBLIC_URL="$url" -e FOLIO_TOKEN="$token" \
	-v "$work/data:/data" "$image" >/dev/null
wait_healthy "$url" >/dev/null || fail "B: /healthz never answered"
owner="$(docker exec "$name-b" stat -c '%U:%G' /data/folio.db)"
[ "$owner" = "folio:folio" ] || fail "B: /data/folio.db is owned by $owner"
[ "$(stat -c %u "$work/data/folio.db")" = 47100 ] || fail "B: host view of the DB owner is not uid 47100"
proc="$(docker exec "$name-b" sh -c 'grep -E "^(Uid|Gid|CapEff|NoNewPrivs):" /proc/1/status')"
grep -Eq '^Uid:[[:space:]]+47100[[:space:]]+47100[[:space:]]+47100[[:space:]]+47100' <<<"$proc" || fail "B: folio is not running as uid 47100: $proc"
grep -Eq '^CapEff:[[:space:]]+0+$' <<<"$proc" || fail "B: folio kept capabilities: $proc"
grep -Eq '^NoNewPrivs:[[:space:]]+1' <<<"$proc" || fail "B: no_new_privs is not set: $proc"
[ "$(docker exec "$name-b" stat -c %U /etc/passwd)" = root ] || fail "B: the planted symlink target was chowned"
docker stop -t 90 "$name-b" >/dev/null

echo "== C: default user with a root-owned bind mount fails clearly"
as_root 'mkdir -m 0755 /w/rootonly'
set +e
out="$(docker run --rm -e FOLIO_PUBLIC_URL=http://127.0.0.1 -e FOLIO_TOKEN="$token" -v "$work/rootonly:/data" "$image" 2>&1)"
code=$?
set -e
if [ "$code" != 1 ] || ! grep -q 'is not writable' <<<"$out"; then
	fail "C: expected exit 1 with a clear message, got $code: $out"
fi

echo "docker smoke: PASS"
