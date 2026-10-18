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

- MCP server over Streamable HTTP with static bearer tokens: 24 tools for
  templates, brands, assets and documents, guides, resources and prompts.
- Rendering of branded PDF (typst) and editable DOCX (pandoc) from Markdown,
  metadata and typed data, with PNG previews and post-render checks.
- Components with exact decimal money: budget, milestones with payment
  schedules, deliverables, signatures and document info.
- Versioned templates and brands, document revisions, publish, trash,
  restore and purge, idempotent renders.
- Starter brand and five starter templates (proposal, requirements, quote,
  technical report, statement of work).
- Sandboxed pandoc and typst (rlimits, timeouts, no network, Landlock when
  available).
- Public capability download links, `/healthz`, retention, blob garbage
  collection and SQLite snapshots.
- Optional advisory AI visual reviewer through OpenRouter.
- Docker image for linux/amd64 and linux/arm64, docker compose file and
  Railway guide.
- Agent Skill (`skills/folio`) and Claude Code plugin (`.claude-plugin`).

[Unreleased]: https://github.com/vocdoni/folio/commits/main
