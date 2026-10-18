# Folio

Folio is a self-hosted document service for AI agents. An agent sends
Markdown, metadata and typed data over [MCP](https://modelcontextprotocol.io);
Folio checks its structure and renders a branded PDF and an editable DOCX
from shared, versioned templates and brands. It returns download links, page
previews and structured, actionable errors.

- **One tool call per document**: `render_document` returns PDF and DOCX
  links, page previews and warnings.
- **Your content, verbatim**: Folio never changes your content or figures.
  Budgets, total rows and payment terms are laid out exactly as the agent
  sends them; Folio does not calculate, round or reformat anything.
- **Shared templates and brands**: created, versioned, previewed and
  archived through MCP; five starter templates (proposal, quote,
  requirements, technical report, statement of work) and a starter brand.
- **Document history**: every revision keeps its input, template and brand
  versions, outputs and warnings; documents can be published, trashed,
  restored and purged.
- **Deterministic output**: the same input, template version, brand version
  and Folio version give byte-identical files.
- **Sandboxed rendering**: pandoc and typst run under rlimits, Landlock (when
  the kernel supports it), timeouts and no network access.
- **Optional AI review**: a vision model can look at rendered pages and
  return advisory findings.
- **Agent friendly**: a Claude Code plugin, a portable Agent Skill, MCP
  guides, resources and prompts.

Folio is licensed under the GNU Affero General Public License v3.0 only
(see [LICENSE](LICENSE)).

## Contents

- [How it works](#how-it-works)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Connecting agents](#connecting-agents)
- [Web UI](#web-ui)
- [Worked examples](#worked-examples)
- [Upgrading from the money-computing contract](#upgrading-from-the-money-computing-contract)
- [Security model](#security-model)
- [Backups and restore](#backups-and-restore)
- [Development](#development)
- [License](#license)

## How it works

```
 agents (Claude Code, Codex, Cursor, Gemini CLI, ...)
            │  MCP over Streamable HTTP, Authorization: Bearer <token>
            ▼
 ┌──────────────────────── folio (single Go binary) ─────────────────────────┐
 │ /mcp ─► auth ─► MCP tools, resources, prompts, guides                      │
 │            catalog (brands, templates) · documents · assets · AI reviewer  │
 │            render: validate → expand → pandoc → typst → checks             │
 │            store: SQLite + content-addressed blobs in FOLIO_DATA_DIR       │
 │ /f/{file_id}/{name}  public downloads (unguessable links, no token)        │
 │ /healthz, /robots.txt                                                      │
 └────────────────────────────────────────────────────────────────────────────┘
```

Markdown is parsed by pandoc into an AST; Folio checks it (sections,
components, images, links, placeholders, nesting), replaces component blocks
(`::: folio-budget`) with generated tables built from your data, then pandoc
writes the DOCX with the brand's reference document and typst typesets the
PDF and PNG previews with the template's layout. Post-render checks flag
blank pages, content touching the page edge, and invalid files.

MCP is the only management interface: there is no web UI. Everyone with a
token sees and can change every brand, template and document on the
instance; the audit log records who did what.

## Quick start

You need a token: 32 to 256 characters of `[A-Za-z0-9._~+/=-]`.

```sh
export FOLIO_TOKEN="$(openssl rand -hex 32)"
```

### Docker

```sh
docker run -d --name folio -p 127.0.0.1:8080:8080 \
  -e FOLIO_PUBLIC_URL=http://localhost:8080 \
  -e FOLIO_TOKEN="$FOLIO_TOKEN" \
  -v folio-data:/data \
  ghcr.io/vocdoni/folio:latest

curl http://localhost:8080/healthz
# {"status":"ok","version":"…","sandbox":"landlock+rlimits","schema_version":2,"reviewer":false}
```

On first start Folio seeds the starter brand and templates. Then connect an
agent (for Claude Code:
`claude mcp add --transport http folio http://localhost:8080/mcp --header "Authorization: Bearer $FOLIO_TOKEN"`)
and ask it to "render a proposal with Folio".

The image runs as the unprivileged user `folio` (uid 47100). A named volume
works as is. For a bind mount owned by root, start the container as root
(`--user 0`): the entrypoint gives the data directory to `folio` and drops
privileges before Folio starts.

### docker compose

```sh
curl -fsSLO https://raw.githubusercontent.com/vocdoni/folio/main/docker-compose.yml
curl -fsSL -o .env https://raw.githubusercontent.com/vocdoni/folio/main/.env.example
printf 'FOLIO_TOKEN=%s\n' "$FOLIO_TOKEN" >> .env
docker compose up -d
```

`docker-compose.yml` publishes port 8080 on `127.0.0.1` with a named volume
`folio-data`; edit `.env` for any setting in [Configuration](#configuration).

### Railway

Create a service from the image `ghcr.io/vocdoni/folio:latest`, attach a
volume at `/data`, set `FOLIO_PUBLIC_URL`, `FOLIO_TOKEN` (or `FOLIO_TOKENS`),
`RAILWAY_RUN_UID=0` and `RAILWAY_DEPLOYMENT_DRAINING_SECONDS=90`, and add a
domain. Step by step: [docs/deploy-railway.md](docs/deploy-railway.md).

### From source

Requirements: Go 1.27, and the official static builds of
[pandoc 3.12.1](https://github.com/jgm/pandoc/releases/tag/3.12.1) and
[typst 0.15.1](https://github.com/typst/typst/releases/tag/v0.15.1) on `PATH`
(or set `FOLIO_PANDOC` and `FOLIO_TYPST` to their paths). Folio refuses to
start if pandoc cannot write a DOCX under `--sandbox`, which only the
official static pandoc build can do.

```sh
git clone https://github.com/vocdoni/folio && cd folio
make build
FOLIO_PUBLIC_URL=http://localhost:8080 FOLIO_DATA_DIR=./data ./bin/folio serve
```

`folio render -request examples/render-request.json -out ./out` renders a
document offline without a database (handy when working on layouts).

## Configuration

Folio is configured only through environment variables. Startup fails with
a message listing every problem when the configuration is invalid.

| Variable | Default | Meaning |
|---|---|---|
| `FOLIO_ADDR` | `:$PORT` if `PORT` is set, else `:8080` | listen address |
| `FOLIO_PUBLIC_URL` | required | base URL used to build download links, e.g. `https://folio.example.com` |
| `FOLIO_DATA_DIR` | `/data` | SQLite database, blobs, backups and the runtime files |
| `FOLIO_TOKEN` | — | single access token (recorded as actor `default`) |
| `FOLIO_TOKENS` | — | comma-separated `name:token` pairs; names match `^[a-z0-9][a-z0-9-]{0,31}$` and are recorded as authors; every token (here and in `FOLIO_TOKEN`) is 32–256 characters of `[A-Za-z0-9._~+/=-]` |
| `FOLIO_INSECURE_NO_AUTH` | `false` | serve `/mcp` without a token (local development only; logs a warning every minute) |
| `FOLIO_SEED` | `true` | seed the starter brand and templates into an empty database, and upgrade the starters Folio created when a new version ships them |
| `FOLIO_RENDER_CONCURRENCY` | `2` | parallel render jobs; about (container memory − 1 GiB) / `FOLIO_RENDER_MEMORY` |
| `FOLIO_RENDER_QUEUE` | `32` | waiting jobs before calls fail with `busy` |
| `FOLIO_RENDER_TIMEOUT` | `60s` | wall-clock limit per pandoc or typst run |
| `FOLIO_RENDER_MEMORY` | `1GiB` | address-space limit per pandoc or typst run |
| `FOLIO_MAX_MARKDOWN_BYTES` | `1MiB` | Markdown size limit |
| `FOLIO_MAX_NESTING` | `32` | maximum block nesting depth (lists, quotes, divs) |
| `FOLIO_MAX_ASSET_BYTES` | `10MiB` | maximum uploaded asset size |
| `FOLIO_MAX_REQUEST_BYTES` | `16MiB` | MCP request body limit |
| `FOLIO_MAX_PAGES` | `200` | maximum rendered pages |
| `FOLIO_MAX_TABLE_CELLS` | `10000` | maximum cells per table |
| `FOLIO_TRASH_RETENTION` | `720h` | trashed documents are purged after this |
| `FOLIO_PREVIEW_RETENTION` | `24h` | template preview renders expire after this |
| `FOLIO_PREVIEW_PAGES_STORED` | `3` | preview PNG pages stored per revision (later pages are rendered on demand) |
| `FOLIO_BLOB_GC_GRACE` | `1h` | unreferenced blobs younger than this are kept; must exceed 3 × `FOLIO_RENDER_TIMEOUT` |
| `FOLIO_BACKUP_INTERVAL` | `24h` | SQLite snapshots in `$FOLIO_DATA_DIR/backups`; `0` disables them |
| `FOLIO_BACKUP_KEEP` | `7` | snapshots kept |
| `FOLIO_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `FOLIO_LOG_FORMAT` | `json` | `json` or `text` |
| `FOLIO_OPENROUTER_API_KEY` | — | enables the optional AI reviewer |
| `FOLIO_REVIEW_MODEL` | `google/gemma-4-31b-it` | OpenRouter model used by the reviewer |
| `FOLIO_REVIEW_MAX_PAGES` | `20` | pages per review call |
| `FOLIO_OPENROUTER_BASE_URL` | `https://openrouter.ai/api/v1` | OpenRouter API root; change it only for tests or an outbound proxy |
| `FOLIO_UI_PASSWORD` | — | enables the [web UI](#web-ui) at `/ui` (at least 12 characters); unset, `/ui` returns 404 |
| `FOLIO_UI_SESSION_TTL` | `12h` | web UI sessions expire after this much inactivity |
| `FOLIO_UI_PREVIEW_CONCURRENCY` | `1` | render slots for web UI previews and example-file conversion (separate from `FOLIO_RENDER_CONCURRENCY`) |
| `FOLIO_TRUSTED_PROXIES` | private and loopback ranges | comma-separated CIDRs whose `X-Forwarded-For` is trusted for login rate limiting; `none` trusts no proxy |
| `FOLIO_ACTIVITY_RETENTION` | `720h` | activity feed entries older than this are deleted |
| `FOLIO_AI_MODEL` | `FOLIO_REVIEW_MODEL` | OpenRouter model for the web UI assistants (vision-capable for PDF examples and logos) |
| `FOLIO_AI_DAILY_CALLS` | `200` | web UI assistant jobs per UTC day for the whole instance |
| `FOLIO_AI_SESSION_CALLS` | `30` | web UI assistant jobs per signed-in session per hour |
| `FOLIO_PANDOC`, `FOLIO_TYPST` | found on `PATH` | paths of the pandoc and typst binaries (the image ships both) |

Sizes accept `KiB`, `MiB`, `GiB` or a byte count; durations are Go
durations (`90s`, `24h`). `PORT` is honoured for platforms that set it.

## Connecting agents

Folio speaks MCP over Streamable HTTP at `https://<your-host>/mcp` and needs
the header `Authorization: Bearer <token>`. The examples use
`https://folio.example.com`; replace it with your `FOLIO_PUBLIC_URL`.

### Claude Code (plugin)

The plugin connects the MCP server and installs the Folio skill. It asks for
the URL and the token (stored as a secret) when you install it:

```text
/plugin marketplace add vocdoni/folio
/plugin install folio@folio
```

### Claude Code (MCP only)

```sh
claude mcp add --transport http folio https://folio.example.com/mcp \
  --header "Authorization: Bearer $FOLIO_TOKEN"
```

Add `--scope user` to make it available in every project. The skill can be
copied separately: `cp -r skills/folio ~/.claude/skills/`.

### Codex

`~/.codex/config.toml`:

```toml
[mcp_servers.folio]
url = "https://folio.example.com/mcp"
bearer_token_env_var = "FOLIO_TOKEN"
```

### Cursor

`~/.cursor/mcp.json` (or `.cursor/mcp.json` in a project):

```json
{
  "mcpServers": {
    "folio": {
      "url": "https://folio.example.com/mcp",
      "headers": { "Authorization": "Bearer ${env:FOLIO_TOKEN}" }
    }
  }
}
```

### Gemini CLI

`~/.gemini/settings.json`:

```json
{
  "mcpServers": {
    "folio": {
      "httpUrl": "https://folio.example.com/mcp",
      "headers": { "Authorization": "Bearer ${FOLIO_TOKEN}" }
    }
  }
}
```

or `gemini mcp add --transport http --header "Authorization: Bearer $FOLIO_TOKEN" folio https://folio.example.com/mcp`.

### Other agents and the Agent Skill

Any MCP client that can send a static `Authorization` header works. Agents
that support [Agent Skills](https://agentskills.io) can load
[`skills/folio`](skills/folio) (`SKILL.md` plus references generated from
the same guides the server returns from `get_guide`).

### claude.ai and Claude Desktop (custom connectors)

Where your Claude plan offers request-header authentication for custom connectors
(an organisation Owner adds the connector and enters a static credential; see
Anthropic's documentation on [authentication for connectors](https://claude.com/docs/connectors/building/authentication#static-credentials-in-request-headers)),
add a custom connector with the URL `https://folio.example.com/mcp` and the header
`Authorization: Bearer <token>`. Without that option a custom connector can only
use OAuth, which Folio does not implement.

### Unsupported clients

Clients that cannot send a static bearer header cannot connect, because Folio does
not implement OAuth: ChatGPT connectors, and any custom-connector setup that offers
OAuth only.

## Web UI

Set `FOLIO_UI_PASSWORD` (at least 12 characters) and open
`https://folio.example.com/ui/` (the root redirects there). Without the
variable `/ui` does not exist. Everyone signs in with that one password;
there are no user accounts.

- **Overview and Activity.** Every MCP tool call (reads included) and every
  change made in the UI, live: who, what, on which item, how it ended and
  how long it took. Entries hold ids, names, outcomes and durations only,
  never document content or tokens, and are deleted after
  `FOLIO_ACTIVITY_RETENTION`.
- **Documents.** Every rendered document as a page proof. Read it page by
  page, download the PDF or DOCX, copy the share link, see revisions and
  warnings, publish, move to the trash, restore or delete permanently.
- **Create.** Paste or upload Markdown, pick a template, fill in the details
  and the tables in a form, render and download. The result is an ordinary
  Folio document, revisable by agents.
- **Templates and the studio.** Edit sections, details, tables, layout
  options, the starting outline, the sample and the labels with a live proof
  of the sample next to the form (it re-renders 800 ms after you stop
  typing). Saving creates a new version; the JSON and a custom Typst layout
  stay available for experts.
- **Brands and images.** Colour pickers, specimens of the bundled fonts,
  logo upload, header and footer, with a live specimen page.
- **Settings.** Version, sandbox level, assistant status and the commands
  that connect an agent to this instance.

### Assistants

With `FOLIO_OPENROUTER_API_KEY` set, the UI offers three assistants (model
`FOLIO_AI_MODEL`, by default the reviewer's model; if drafts are weak, pick
a stronger one):

- **Draft a template** from a description and, optionally, an example
  (Markdown, Word or PDF). The draft is checked and its sample rendered
  exactly as `create_template` would, then opens unsaved in the studio.
- **Fill in from my text** on the Create screen proposes the template, the
  details and the tables for your Markdown. Values that do not appear word
  for word in your text are highlighted for checking; the Markdown itself is
  never changed.
- **Suggest a brand** from an uploaded logo (Folio extracts its colours
  itself) or from a website (Folio reads the page's title, colours and logo
  candidates through the same guarded fetcher as asset URLs; a logo is
  downloaded only when you pick it).

Assistant answers are proposals: nothing is saved until you save it. What
you hand an assistant (text, an example file, a logo, a website's colours)
is sent to OpenRouter with data collection denied, only when you ask.
Limits: `FOLIO_AI_DAILY_CALLS` per day for the instance and
`FOLIO_AI_SESSION_CALLS` per hour for each sign-in; at most one request runs
per session and four per instance.

### Security

- The password is compared in constant time; sign-in attempts are limited
  to five a minute per client address and sixty a minute in total. Behind a
  proxy, `X-Forwarded-For` is trusted only from `FOLIO_TRUSTED_PROXIES`.
- Sessions are random 256-bit ids kept in memory (a restart signs everyone
  out), in an `HttpOnly`, `SameSite=Strict` cookie scoped to `/ui` and
  `Secure` when `FOLIO_PUBLIC_URL` is https; they expire after
  `FOLIO_UI_SESSION_TTL` of inactivity.
- Every change needs the `X-Folio-UI` header and a same-origin `Origin`.
  Pages carry a strict Content-Security-Policy (`'self'` only, no inline
  script or style, no framing). The UI loads nothing from other sites.
- Template previews are rendered in a separate queue
  (`FOLIO_UI_PREVIEW_CONCURRENCY`) so the UI never takes a render slot from
  agents, and kept in memory for ten minutes, never stored.


## Worked examples

The examples show the arguments of each MCP tool call as JSON, which is what
an agent sends; you normally just ask your agent in plain language. They
run against a fresh instance with the starter content (the test suite
executes every example in this section).

Every tool result has a one-line text summary, the same data as
`structuredContent`, and, for renders, links to the files. Failed calls
return `isError: true` with `structuredContent.error = {code, message,
retryable, details[]}`; each detail has a JSON pointer `path` and a `hint`.

### Render a proposal from the starter template

1. `list_templates` and `get_template` with `{"id": "proposal"}` show the
   template's sections, schemas, skeleton and a complete sample.
2. Validate the document (a dry run; nothing is stored). Folio never
   changes your content or figures: send every value exactly as it should appear,
   including the total rows and payments:

<!-- folio-example tool=validate_document -->
```json
{
  "template_id": "proposal",
  "metadata": {
    "title": "Online shop redesign", "date": "2026-10-18", "language": "en",
    "client": "Northwind Traders", "project": "Shop redesign",
    "reference": "P-2026-07", "valid_until": "2026-11-30"
  },
  "markdown": "# Executive summary\n\nNorthwind Traders wants a faster online shop. We propose a redesign delivered in three milestones.\n\n::: folio-info\n:::\n\n# Scope\n\n- Discovery workshop with the sales team\n- Design system and storefront templates\n- Migration of 300 product pages\n\n# Plan\n\n::: folio-milestones\n:::\n\n# Deliverables\n\n::: folio-deliverables\n:::\n\n# Budget\n\n::: folio-budget\n:::\n\n# Acceptance\n\n::: folio-signatures\n:::\n",
  "data": {
    "budget": {
      "items": [
        {"description": "Discovery workshop", "quantity": "2", "unit": "day", "unit_price": "€950.00", "amount": "€1,900.00"},
        {"description": "Design and build", "quantity": "18", "unit": "day", "unit_price": "€720.00", "discount": "5%", "amount": "€12,312.00"},
        {"description": "Hosting (VAT exempt)", "quantity": "12", "unit": "month", "unit_price": "€45.00", "amount": "€540.00"}
      ],
      "totals": [
        {"label": "Subtotal", "amount": "€14,752.00"},
        {"label": "VAT (21% on €14,212.00)", "amount": "€2,984.52"},
        {"label": "Total", "amount": "€17,736.52", "emphasis": true}
      ]
    },
    "milestones": [
      {"name": "Kick-off", "date": "2026-11-02", "deliverables": ["D1"], "payment": "30% · €5,320.96"},
      {"name": "Beta", "date": "2026-12-14", "deliverables": ["D2"], "payment": "40% · €7,094.61"},
      {"name": "Launch", "date": "2027-01-25", "deliverables": ["D3"], "payment": "30% · €5,320.95"}
    ],
    "deliverables": [
      {"id": "D1", "name": "Discovery report", "acceptance": "Approved by the sales lead"},
      {"id": "D2", "name": "Beta storefront", "acceptance": "All product pages migrated"},
      {"id": "D3", "name": "Production launch", "acceptance": "Live on the shop domain"}
    ],
    "signatures": [
      {"name": "Dana Whitfield", "role": "COO", "organization": "Northwind Traders"},
      {"name": "Alex Smith", "role": "Account manager", "organization": "Example Co"}
    ]
  }
}
```

   The result is `{"valid": true, "findings": [], "template": {...}, "brand": {...}}`.
   Folio checked sections, components, schemas and that every milestone's
   deliverable ids exist; it did not check the arithmetic. A JSON number in a
   figure field fails with `schema_violation` and the hint "send figures as
   strings exactly as they should appear".

3. Render it with the same arguments plus an `idempotency_key`, so a retry
   after a dropped connection returns the same document:

<!-- folio-example tool=render_document from=validate_document -->
```json
{"idempotency_key": "northwind-shop-proposal-2026-10-18", "include_previews": "first"}
```

   (This block lists only the arguments added to the validated call.) The
   result (abridged):

```json
{
  "document_id": "019a0c3e-5f1b-7c2d-9e8f-0a1b2c3d4e5f",
  "revision": 1,
  "unchanged": false,
  "status": "draft",
  "template": {"id": "proposal", "version": 1},
  "brand": {"id": "example", "version": 1},
  "files": [
    {"kind": "pdf", "name": "online-shop-redesign.pdf", "url": "https://folio.example.com/f/3f2b…/online-shop-redesign.pdf", "mime": "application/pdf", "size": 62493, "sha256": "…", "pages": 3},
    {"kind": "docx", "name": "online-shop-redesign.docx", "url": "https://folio.example.com/f/8c41…/online-shop-redesign.docx", "mime": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "size": 22778, "sha256": "…"}
  ],
  "previews": ["https://folio.example.com/f/a7d0…/online-shop-redesign-page-1.png", "…page-2.png", "…page-3.png"],
  "warnings": []
}
```

### Create a brand and a template

Upload the logo (SVG is sharpest; Folio adds a PNG rendition for DOCX):

<!-- folio-example tool=upload_asset -->
```json
{
  "name": "acme-logo.svg",
  "description": "ACME logo, teal",
  "content_base64": "PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNDAiIGhlaWdodD0iNjAiPjxyZWN0IHdpZHRoPSIyNDAiIGhlaWdodD0iNjAiIHJ4PSI4IiBmaWxsPSIjMEI3QTc1Ii8+PGNpcmNsZSBjeD0iMzAiIGN5PSIzMCIgcj0iMTgiIGZpbGw9IiNGRkZGRkYiLz48L3N2Zz4="
}
```

Create the brand with the returned `asset_id`:

<!-- folio-example tool=create_brand -->
```json
{
  "brand": {
    "id": "acme",
    "name": "ACME Studio",
    "colors": {"primary": "#0B7A75", "secondary": "#14213D", "accent": "#FCA311",
               "text": "#111111", "muted": "#666666", "background": "#FFFFFF"},
    "fonts": {"heading": "Inter", "body": "Source Serif 4", "mono": "JetBrains Mono"},
    "logo": {"asset_id": "<asset_id>", "width_mm": 30},
    "header": {"text": "ACME Studio", "show_logo": true},
    "footer": {"text": "ACME Studio · acme.example", "legal": "ACME Studio S.L.", "page_numbers": true},
    "docx": {"font_fallbacks": {"heading": "Arial", "body": "Georgia", "mono": "Courier New"}}
  },
  "note": "initial brand"
}
```

Create a template. Folio validates the spec and renders its `sample`; the
call fails if the sample produces an error or a warning other than a
placeholder:

<!-- folio-example tool=create_template -->
```json
{
  "template": {
    "id": "acme-quote",
    "name": "ACME quote",
    "description": "One-page quote with a budget.",
    "category": "quote",
    "brand_id": "acme",
    "languages": ["en", "es"],
    "layout": {"base": "quote", "typst": null,
               "options": {"cover": false, "toc": false, "page_size": "a4", "heading_numbering": false}},
    "metadata_schema": {"type": "object", "required": ["client"],
                        "properties": {"client": {"type": "string", "minLength": 1}}},
    "data_schema": {"type": "object", "required": ["budget"],
                    "properties": {"budget": {"$ref": "folio:component/budget"}}},
    "sections": [
      {"title": "Summary", "required": true, "aliases": ["Resumen"]},
      {"title": "Budget", "required": true, "aliases": ["Presupuesto"], "component": "budget"}
    ],
    "components": ["budget", "info"],
    "strict": false,
    "body_skeleton": "# Summary\n\nTODO\n\n::: folio-info\n:::\n\n# Budget\n\n::: folio-budget\n:::\n",
    "sample": {
      "markdown": "# Summary\n\nWe will deliver the work described below.\n\n::: folio-info\n:::\n\n# Budget\n\n::: folio-budget\n:::\n",
      "metadata": {"title": "Sample quote", "date": "2026-10-18", "language": "en", "client": "Example client"},
      "data": {"budget": {"items": [{"description": "Design", "quantity": "2", "unit": "day", "unit_price": "€1,500.00", "amount": "€3,000.00"}],
                          "totals": [{"label": "VAT (21%)", "amount": "€630.00"},
                                     {"label": "Total", "amount": "€3,630.00", "emphasis": true}]}}
    }
  },
  "note": "first version"
}
```

Look at every page of the sample:

<!-- folio-example tool=preview_template -->
```json
{"id": "acme-quote", "include_previews": "all"}
```

Change it later with `update_template` and `"expected_version": 1` (the
version you read); a stale version fails with `conflict`.

### Revise and publish a document

Render a first quote with the new template:

<!-- folio-example tool=render_document -->
```json
{
  "template_id": "acme-quote",
  "idempotency_key": "quote-001",
  "metadata": {"title": "Website maintenance", "date": "2026-10-18", "language": "en", "client": "Globex"},
  "markdown": "# Summary\n\nMonthly maintenance of the Globex website.\n\n::: folio-info\n:::\n\n# Budget\n\n::: folio-budget\n:::\n",
  "data": {"budget": {"items": [{"description": "Maintenance", "quantity": "12", "unit": "month", "unit_price": "€300.00", "amount": "€3,600.00"}],
                      "totals": [{"label": "VAT (21%)", "amount": "€756.00"},
                                 {"label": "Total", "amount": "€4,356.00", "emphasis": true}]}}
}
```

A colleague (or you, later) reads the exact input of the latest revision:

<!-- folio-example tool=get_document -->
```json
{"id": "<document_id>", "include_source": true, "include_previews": "none"}
```

and renders a new revision with `document_id` and `expected_revision` (the
`latest_revision` just read). If someone revised it in the meantime, the
call fails with `conflict`:

<!-- folio-example tool=render_document -->
```json
{
  "document_id": "<document_id>",
  "expected_revision": 1,
  "metadata": {"title": "Website maintenance", "date": "2026-10-18", "language": "en", "client": "Globex"},
  "markdown": "# Summary\n\nMonthly maintenance of the Globex website, including security updates.\n\n::: folio-info\n:::\n\n# Budget\n\n::: folio-budget\n:::\n",
  "data": {"budget": {"items": [{"description": "Maintenance", "quantity": "12", "unit": "month", "unit_price": "€300.00", "amount": "€3,600.00"}],
                      "totals": [{"label": "Subtotal", "amount": "€3,600.00"},
                                 {"label": "Discount (10%)", "amount": "−€360.00"},
                                 {"label": "VAT (21%)", "amount": "€680.40"},
                                 {"label": "Total", "amount": "€3,920.40", "emphasis": true}]}}
}
```

Mark revision 2 as final:

<!-- folio-example tool=publish_document -->
```json
{"id": "<document_id>", "revision": 2}
```

`delete_document` moves a document to the trash (its links stop working),
`restore_document` brings it back, and `delete_document` with
`"purge": true` deletes it for good (published documents also need
`"confirm_published": true`).

### Components and figures

Components are empty fenced divs that Folio fills from `data`:
`::: folio-budget`, `::: folio-milestones`, `::: folio-deliverables`,
`::: folio-signatures` and `::: folio-info`. They take no attributes.
Every displayed value is a JSON string printed exactly as sent
(`"€1,500.00"`, `"1.500,00 €"`, `"TBD"`): a budget is its `items` (each with
an `amount`) plus the `totals` rows you write yourself, and a milestone's
`payment` is plain text. Folio never calculates or checks the figures, so do
the arithmetic before rendering. The `components` guide (`get_guide` with
`{"topic": "components"}`) has every field.

## Upgrading from the money-computing contract

Earlier development builds computed budget totals, taxes and payment
schedules. Folio now lays out exactly what you send:

| before | now |
|---|---|
| `currency`, `tax_rate`, `discount_pct`, `expected_total` in `data.budget` | removed: write the figures in the style you want |
| line `quantity`, `unit_price` (strings or numbers) | strings as they should appear (`"12"`, `"€650.00"`); both optional |
| line `tax_rate`, `discount_pct` | `discount` text when you want a Discount column (`"10%"`) |
| the line amount Folio computed | `amount` per line (required) |
| subtotal, discount, tax and total rows Folio computed | `totals`: `[{"label", "amount", "emphasis"}]` rows, in order |
| milestone `payment_pct` with `::: {.folio-milestones show_payments=true}` | milestone `payment` text (`"30% · €4,500.00"`) and `::: folio-milestones` |
| `computed` in `validate_document`, `render_document`, `get_document` | removed |

- **Starter templates upgrade themselves.** On start, Folio compares the
  starter brand and templates with the ones it ships (the database records
  `seed_version`). A starter whose latest version Folio created gets a new
  version (note "starter upgrade (seed v2)"); one whose latest version a
  user created is left alone and named in a startup warning
  (`starter upgrade skipped`) so you can compare it by hand.
  `FOLIO_SEED=false` turns seeding and these upgrades off.
- **Existing documents keep their files and revisions.** Their stored input
  uses the old data shape, so revising one (`render_document` with
  `document_id`) fails with `validation_failed` until you rewrite
  `data.budget` and `data.milestones` as in the table above.
- **Pinned versions.** `render_document` with `template_version: 1` of an
  upgraded starter fails the same way, and its error message names the current
  version; `validate_document` returns `valid: false` and says so in its text
  summary. Omit `template_version` to use the current version.
- Your own templates keep working when their `data_schema` references
  `folio:component/budget` or `folio:component/milestones` (the reference
  now resolves to the new schema), but their `sample` and the documents
  rendered with them need the new data shape; update the template with
  `update_template`. Template `labels` for `budget.subtotal`, `budget.tax`
  and `budget.total` are still accepted and ignored.

## Security model

- **Tokens.** Static bearer tokens from `FOLIO_TOKEN` / `FOLIO_TOKENS`, at
  least 32 characters, compared in constant time and never logged. All
  tokens have the same rights: a token holder can read and change everything
  on the instance. Rotate a token by changing the variable and restarting.
  Folio has no OAuth, users or roles.
- **Web UI.** Off unless `FOLIO_UI_PASSWORD` is set; one shared password,
  rate-limited sign-in, server-side sessions, CSRF header and Origin
  checks, strict CSP (see [Web UI](#web-ui)).
- **Download links are capabilities.** `/f/<id>/<name>` needs no token: the
  122-bit random id is the only secret. Anyone holding a link can download
  that file. Links are not indexable (`X-Robots-Tag`, `robots.txt`), are not
  leaked through `Referer`, stop working when the document is trashed or
  purged, and template preview links expire after
  `FOLIO_PREVIEW_RETENTION`. Share links only with intended readers.
- **Untrusted content.** Markdown, data, template Typst code and assets are
  treated as untrusted even from token holders. Raw HTML, TeX and Typst in
  Markdown are inert; images must be uploaded assets; data is inserted as
  plain text; SVGs with scripts or external references are rejected; URL
  uploads refuse private, loopback and metadata addresses.
- **Sandbox.** pandoc and typst run as the unprivileged `folio` user through
  `folio sandbox-exec`: memory, CPU, file size and process rlimits, a
  wall-clock timeout, no network (proxy variables point at a closed port and
  only vendored Typst packages are available), and a Landlock filesystem
  and network ruleset when the kernel supports it. `/healthz` and
  `get_capabilities` report the level: `landlock+rlimits` or
  `rlimits-only`.
- **AI reviewer privacy.** Page images leave the instance only when
  `FOLIO_OPENROUTER_API_KEY` is set and `review_document` runs or a
  template is created or updated. The web UI assistants send the text,
  example file, logo or website colours you hand them, only when you ask.
  Requests go to OpenRouter with provider data collection denied; findings
  are advisory and never change a document.
- **Logs** contain tool names, actors, outcomes and durations, never tokens,
  document content or download links.

Report vulnerabilities as described in [SECURITY.md](SECURITY.md).

## Backups and restore

Folio writes a consistent SQLite snapshot (`VACUUM INTO`) every
`FOLIO_BACKUP_INTERVAL` to `$FOLIO_DATA_DIR/backups/folio-<UTC time>.db` and
keeps the newest `FOLIO_BACKUP_KEEP`. Rendered files and assets are
content-addressed blobs in `$FOLIO_DATA_DIR/blobs`.

- **Full backup:** copy the whole data directory (or use your platform's
  volume backups) while Folio is stopped, or copy `backups/` and `blobs/`
  while it runs.
- **Restore a snapshot:** stop Folio, then in the data directory
  `cp backups/folio-<time>.db folio.db && rm -f folio.db-wal folio.db-shm`,
  and start Folio. Documents purged after the snapshot was taken may have
  lost their blobs; restore `blobs/` from the same point in time when you
  need them.
- A Folio binary refuses to open a database written by a newer schema
  version; roll back the database together with the image.

## Development

```sh
make build        # bin/folio
make test         # go test ./... (needs pandoc and typst, see below)
make test-race    # with the race detector (needs cgo)
make lint         # golangci-lint
make generate     # regenerate skills/folio from the guides
make docker       # build the image as folio:dev
make docker-smoke # build it and run scripts/docker-smoke.sh
make run          # serve locally with a throwaway token and ./data
make ui           # build the web UI into internal/webui/dist (Node 24)
make ui-check     # svelte-check and the UI unit tests
make dev-ui       # Vite dev server on :5173, API proxied to make run
```

The web UI lives in `web/` (Svelte 5, TypeScript, Vite). `make ui` writes
the build into `internal/webui/dist`, which `make build` embeds; without it
`/ui` answers "UI assets not built". For UI work run `FOLIO_UI_PASSWORD=…
make run` in one terminal and `make dev-ui` in another, then open
`http://localhost:5173/ui/`. The end-to-end tests
(`scripts/ui-e2e.sh <image>`) run Playwright against a container with a
stub OpenRouter.

Tests that render need the pinned pandoc and typst: put them on `PATH` or
set `FOLIO_PANDOC` and `FOLIO_TYPST`. Without them those tests are skipped
with a `SKIPPING:` message. The golden tests (`go test ./internal/render
-run Golden`) compare SHA-256 sums of the starter templates' PDF and DOCX;
after an intended output change run them with `-update` and note it in the
CHANGELOG. The MCP guides in `internal/mcpserver/guides` are the source of
`skills/folio`: edit the guides, then `make generate`.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow and conventions.

## License

Folio is free software: you can redistribute it and/or modify it under the
terms of the GNU Affero General Public License, version 3 only. See
[LICENSE](LICENSE). The image bundles pandoc (GPL-2.0-or-later, run as a
separate program), typst (Apache-2.0) and fonts under the SIL Open Font
License; see [NOTICE](NOTICE).
