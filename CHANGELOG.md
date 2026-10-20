# Changelog

All notable changes to Folio are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The MCP tool names and their input and output fields are the public API.

Rendered bytes are deterministic for a given Folio version. Any change that
alters the PDF or DOCX produced for the same input (pandoc, typst, fonts,
layouts) is listed under "Changed" with the note "output changes".

## [Unreleased]

Planned as 0.1.0, the first release.

### Added

- MCP server over Streamable HTTP with static bearer tokens: 25 tools for
  templates, brands, assets and documents, guides, resources and prompts.
- Rendering of branded PDF (typst) and editable DOCX (pandoc) from Markdown,
  metadata and typed data, with PNG previews and post-render checks.
- Components that lay out content verbatim: budget (line items and the
  caller's total rows), milestones (with payment text), deliverables,
  signatures and document info. Folio never calculates, rounds or reformats
  figures; component values are strings, and JSON numbers are rejected.
- Versioned templates and brands, document revisions, publish, trash,
  restore and purge, idempotent renders.
- Starter brand and five starter templates (proposal, requirements, quote,
  technical report, statement of work), versioned: a new release upgrades
  the starters Folio created and leaves user-edited ones alone with a startup
  warning.
- Sandboxed pandoc and typst (rlimits, timeouts, no network, Landlock when
  available).
- Public capability download links, `/healthz`, retention, blob garbage
  collection and SQLite snapshots.
- Optional advisory AI visual reviewer through OpenRouter.
- Web UI at `/ui` behind `FOLIO_UI_PASSWORD` and/or SSO: documents as page proofs with
  PDF and DOCX downloads, create from Markdown, template studio with live
  proof, brands and images; server-side sessions, CSRF and Origin checks, rate
  limits and a strict CSP.
- AI assistants in the web UI (draft a template, fill in from text, suggest
  a brand) through OpenRouter; proposals only, nothing saved until you save,
  with daily and per-session quotas.
- Activity log and live feed of every MCP tool call and UI change (ids,
  names, outcomes, durations; retention `FOLIO_ACTIVITY_RETENTION`).
- SSO sign-in to the web UI through any OpenID Connect provider, next to or
  instead of the password (`FOLIO_OIDC_ISSUER`, `FOLIO_OIDC_CLIENT_ID`,
  `FOLIO_OIDC_CLIENT_SECRET`, `FOLIO_OIDC_ALLOWED_DOMAINS`,
  `FOLIO_OIDC_ALLOWED_EMAILS`, `FOLIO_OIDC_NAME`; redirect URL
  `<FOLIO_PUBLIC_URL>/ui/api/oidc/callback`): authorization code flow with
  PKCE, verified e-mails from the allowed domains or addresses only, provider
  metadata fetched at the first sign-in so Folio starts while the provider
  is down. Activity rows of SSO sessions name the e-mail as the actor
  (actors now match `^[a-z0-9][a-z0-9._@+-]{0,253}$`), `GET /ui/api/session`
  reports the sign-in methods, Settings shows them, and the session cookie
  is `SameSite=Lax` so it survives the redirect back from the provider.
- Docker image for linux/amd64 and linux/arm64, docker compose file and
  Railway guide.
- Agent Skill (`skills/folio`) and Claude Code plugin (`.claude-plugin`).
- Inline templates: `render_document` and `validate_document` take `template`
  (an inline spec) instead of `template_id`; the spec is stored as a hidden,
  content-addressed template `inline-<hash>` that revisions reuse and
  `save_inline_template` turns into a regular template. `list_documents`
  gains `inline`, documents report `template_inline`, `get_capabilities`
  reports `default_brand` and the limits `max_inline_template_bytes` and
  `max_layout_typst_bytes` (`FOLIO_DEFAULT_BRAND`,
  `FOLIO_MAX_INLINE_TEMPLATE_BYTES`, `FOLIO_MAX_LAYOUT_TYPST_BYTES`); the
  retention pass deletes unreferenced inline templates.

[Unreleased]: https://github.com/vocdoni/folio/commits/main
