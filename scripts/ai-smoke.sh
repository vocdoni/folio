#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Manual smoke test of the web UI assistants against a REAL model (not CI,
# it costs a few cents). Starts a local Folio on a loopback port with a fresh
# data directory, signs in to the UI API and runs: document-plan (without and
# with template_id=proposal), template-draft, brand-suggest from a web page
# and brand-suggest from a generated PNG logo. Prints one line per assistant
# and exits non-zero if any of them fails.
#
#   FOLIO_OPENROUTER_API_KEY=sk-or-... scripts/ai-smoke.sh [https-url]
#
# The URL defaults to https://example.com. Optional environment:
#   FOLIO_AI_MODEL             model for the assistants (server default otherwise)
#   FOLIO_OPENROUTER_BASE_URL  point at a stub (testing this script only)
#   FOLIO_BIN                  use this binary instead of building one
#   AI_SMOKE_PORT              loopback port (default: a free one)
#   AI_SMOKE_DEADLINE          seconds per assistant (default 150)
#
# Needs: go (unless FOLIO_BIN), curl, jq, python3, openssl. The API key is
# read from the environment only; it is never printed or written to disk.
# shellcheck disable=SC2016 # jq programs are single-quoted on purpose
set -euo pipefail

: "${FOLIO_OPENROUTER_API_KEY:?set FOLIO_OPENROUTER_API_KEY to an OpenRouter key (it is never printed)}"
site="${1:-https://example.com}"
deadline="${AI_SMOKE_DEADLINE:-150}"
root="$(cd "$(dirname "$0")/.." && pwd)"
for tool in curl jq python3 openssl; do
	command -v "$tool" >/dev/null || { echo "ai-smoke: $tool is required" >&2; exit 2; }
done

tmproot="${TMPDIR:-$HOME/.cache/folio/tmp}"
mkdir -p "$tmproot"
work="$(mktemp -d "$tmproot/ai-smoke.XXXXXX")"
pid=""

# shellcheck disable=SC2329 # invoked by the EXIT trap
cleanup() {
	if [ -n "$pid" ]; then
		kill "$pid" >/dev/null 2>&1 || true
		wait "$pid" >/dev/null 2>&1 || true
	fi
	chmod -R u+w "$work" >/dev/null 2>&1 || true # the runtime copy is read-only
	rm -rf "$work"
}
trap cleanup EXIT

bin="${FOLIO_BIN:-}"
if [ -z "$bin" ]; then
	bin="$work/folio"
	(cd "$root" && CGO_ENABLED=0 go build -trimpath -o "$bin" ./cmd/folio)
fi

port="${AI_SMOKE_PORT:-$(python3 -I -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')}"
url="http://127.0.0.1:$port"
password="$(openssl rand -hex 16)" # random per run, never printed
token="$(openssl rand -hex 24)"
mkdir -p "$work/data"

FOLIO_ADDR="127.0.0.1:$port" FOLIO_PUBLIC_URL="$url" FOLIO_DATA_DIR="$work/data" \
	FOLIO_TOKEN="$token" FOLIO_UI_PASSWORD="$password" FOLIO_LOG_LEVEL=warn \
	"$bin" serve >"$work/server.log" 2>&1 &
pid=$!

ready=""
for _ in $(seq 1 60); do
	if curl -fsS "$url/healthz" >/dev/null 2>&1; then ready=1; break; fi
	kill -0 "$pid" 2>/dev/null || break
	sleep 0.5
done
if [ -z "$ready" ]; then
	echo "ai-smoke: folio did not start; server log follows" >&2
	sed -e 's/sk-or-[A-Za-z0-9_-]*/sk-or-***/g' "$work/server.log" | tail -30 >&2
	exit 1
fi

jar="$work/cookies"
# api METHOD PATH [curl args...]: a UI API call with the CSRF header and Origin.
api() {
	local method="$1" path="$2"
	shift 2
	curl -sS -X "$method" -b "$jar" -c "$jar" -H "X-Folio-UI: 1" -H "Origin: $url" "$@" "$url$path"
}

post_json() { # PATH jq-args...: POST the object built by `jq -n ARGS`
	jq -n "${@:2}" | api POST "$1" -H 'Content-Type: application/json' -d @-
}

login="$(post_json /ui/api/login --arg p "$password" '{password: $p}')"
if [ "$(jq -r '.authenticated // false' <<<"$login")" != "true" ]; then
	echo "ai-smoke: sign-in failed: $(jq -r "(.error // .) | .message // \"unexpected answer\"" <<<"$login")" >&2
	exit 1
fi
status="$(api GET /ui/api/ai/status)"
if [ "$(jq -r '.enabled' <<<"$status")" != "true" ]; then
	echo "ai-smoke: the assistants are off on the server" >&2
	exit 1
fi
echo "ai-smoke: model $(jq -r '.model // "?"' <<<"$status"), page $site"

# Fixtures: a Markdown quote with a budget table, and a generated PNG logo.
plan_md='# Showroom refit quote

Prepared for Northwind Traders, reference NW-2026-031, valid until 30 November 2026.

## Budget

| Item | Qty | Unit price | Amount |
|---|---|---|---|
| 55-inch display screens | 6 | EUR 1,150.00 | EUR 6,900.00 |
| Mounting and installation (days) | 2 | EUR 640.00 | EUR 1,280.00 |

Total: EUR 8,180.00 excluding VAT.
'
python3 -I - "$work/logo.png" <<'PY'
import struct, sys, zlib
w, h = 256, 128
rows = []
for y in range(h):
    row = bytearray([0])
    for x in range(w):
        if 24 <= x < 104 and 24 <= y < 104:
            px = (0x0B, 0x5D, 0x4B)  # teal square
        elif 128 <= x < 232 and 48 <= y < 80:
            px = (0xD9, 0x8E, 0x04)  # amber bar
        else:
            px = (255, 255, 255)
        row += bytes(px)
    rows.append(bytes(row))
def chunk(t, d):
    c = struct.pack(">I", len(d)) + t + d
    return c + struct.pack(">I", zlib.crc32(t + d) & 0xFFFFFFFF)
png = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
       + chunk(b"IDAT", zlib.compress(b"".join(rows))) + chunk(b"IEND", b""))
open(sys.argv[1], "wb").write(png)
PY

failures=0

# finish NAME STARTED_JSON JQ_SUMMARY BEGIN: polls the job named in the start
# answer, prints one line and, for a done job, a short summary.
finish() {
	local name="$1" started="$2" summary="$3" begin="$4" id out state elapsed
	id="$(jq -r '.job_id // empty' <<<"$started")"
	if [ -z "$id" ]; then
		printf '%-24s %-7s %4ss  %s: %s\n' "$name" error 0 "$(jq -r "(.error // .) | .code // \"?\"" <<<"$started")" "$(jq -r "(.error // .) | .message // \"no job started\"" <<<"$started")"
		failures=$((failures + 1))
		return
	fi
	while :; do
		out="$(api GET "/ui/api/ai/jobs/$id")"
		state="$(jq -r '.state // "error"' <<<"$out")"
		[ "$state" = "running" ] || [ "$state" = "queued" ] || break
		if [ $((SECONDS - begin)) -ge "$deadline" ]; then
			state="timeout"
			break
		fi
		sleep 2
	done
	elapsed=$((SECONDS - begin))
	case "$state" in
	done)
		printf '%-24s %-7s %4ss\n' "$name" "done" "$elapsed"
		printf '    %s\n' "$(jq -r "$summary" <<<"$out")"
		;;
	timeout)
		printf '%-24s %-7s %4ss  client deadline of %ss reached\n' "$name" timeout "$elapsed" "$deadline"
		failures=$((failures + 1))
		;;
	*)
		printf '%-24s %-7s %4ss  %s: %s\n' "$name" "$state" "$elapsed" "$(jq -r '.error.code // "?"' <<<"$out")" "$(jq -r '.error.message // .message // "unknown error"' <<<"$out")"
		failures=$((failures + 1))
		;;
	esac
}

plan_summary='.result | "template \(.template_id), \(.metadata | length) metadata keys, \(.inferred | length) inferred, \(.warnings | length) warnings"'
draft_summary='.result | "draft \(.spec.id), ok=\(.ok), \(.findings | length) findings, repaired=\(.repaired)"'
brand_summary='.result | "brand \(.brand.name), primary \(.brand.colors.primary), \((.evidence.images // []) | length) logo candidates, \((.swatches // []) | length) swatches"'

begin=$SECONDS
started="$(post_json /ui/api/ai/document-plan --arg m "$plan_md" '{markdown: $m}')"
finish "document-plan" "$started" "$plan_summary" "$begin"

begin=$SECONDS
started="$(post_json /ui/api/ai/document-plan --arg m "$plan_md" '{markdown: $m, template_id: "proposal"}')"
finish "document-plan (proposal)" "$started" "$plan_summary" "$begin"

begin=$SECONDS
started="$(post_json /ui/api/ai/template-draft --arg d 'A one-page meeting minutes template with attendees, decisions and an action items table.' '{description: $d}')"
finish "template-draft" "$started" "$draft_summary" "$begin"

begin=$SECONDS
started="$(post_json /ui/api/ai/brand-suggest --arg u "$site" '{url: $u}')"
finish "brand-suggest (web)" "$started" "$brand_summary" "$begin"

asset="$(api POST /ui/api/assets -F "name=ai-smoke-logo" -F "file=@$work/logo.png;type=image/png")"
asset_id="$(jq -r '.asset_id // empty' <<<"$asset")"
begin=$SECONDS
if [ -n "$asset_id" ]; then
	started="$(post_json /ui/api/ai/brand-suggest --arg a "$asset_id" '{asset_id: $a}')"
else
	started="$asset" # the upload error, reported as this assistant's failure
fi
finish "brand-suggest (logo)" "$started" "$brand_summary" "$begin"

if [ "$failures" -ne 0 ]; then
	echo "ai-smoke: $failures assistant(s) failed" >&2
	exit 1
fi
echo "ai-smoke: all assistants passed"
