# Contributing to Folio

Thanks for helping. Bug reports, documentation fixes, new starter templates
and code are all welcome. By participating you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Before you start

- Search the existing issues first. For anything larger than a small fix,
  open an issue describing the problem and the change you propose, so we can
  agree on the approach before you write code.
- Security problems are never reported in public issues: see
  [SECURITY.md](SECURITY.md).

## Development setup

1. Go 1.27 (see `go.mod`).
2. The official static builds of pandoc 3.12.1 and typst 0.15.1, on `PATH`
   or pointed to by `FOLIO_PANDOC` and `FOLIO_TYPST`. Rendering tests are
   skipped with a `SKIPPING:` message without them; CI always runs them.
3. golangci-lint v2.15.0 for `make lint`.
4. Docker, for `make docker` and `make docker-smoke`.

```sh
make build test lint
```

See the README "Development" section for every make target.

## Making a change

- Keep changes focused: one topic per pull request.
- Add or update tests with the change. Bug fixes start with a test that
  fails without the fix.
- Run `make test`, `make test-race` and `make lint` before pushing.
- Agent-facing documentation lives in `internal/mcpserver/guides/*.md`
  (served by `get_guide`). After editing a guide run `make generate` and
  commit the regenerated `skills/folio`; CI fails when they differ.
- If your change alters rendered output, regenerate the goldens
  (`go test ./internal/render -run Golden -update`) and say so in
  `CHANGELOG.md`: output bytes are part of Folio's determinism promise.
- MCP tool names, input fields and output fields are a public API. Changes
  must be additive within a major version.
- Add an entry under "Unreleased" in `CHANGELOG.md` for user-visible
  changes.

## Code conventions

- Idiomatic Go, `CGO_ENABLED=0`, `log/slog`, `context` on every blocking
  call, errors wrapped with `%w`, no panics on external input.
- Folio never changes content or figures: component values are rendered
  verbatim, with no numeric parsing in `internal/components` (a test and the
  linter enforce it).
- Never log tokens, document content or download links.
- Every external process runs through `folio sandbox-exec` with a timeout.
- Every Go file starts with `// SPDX-License-Identifier: AGPL-3.0-only`;
  shell, YAML, SQL, Typst and Lua files carry the same line as a comment.
- No organisation-specific names, URLs or branding in code or starter
  content.

## Commits and pull requests

- Conventional commits: `type(scope): lowercase summary`, for example
  `fix(render): keep table headers with their first row`. Types: `feat`,
  `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, `chore`.
- Pull requests describe the problem, the change and how it was tested. CI
  must be green: secret scan, lint, tests, vulnerability check, generated
  files, and the Docker smoke test.

## Licence

Folio is licensed under the GNU Affero General Public License v3.0 only.
By contributing you agree that your contributions are licensed under the
same terms.
