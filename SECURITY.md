# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: open the
repository's **Security** tab and choose **Report a vulnerability**. Do not
open a public issue or pull request for a security problem.

Include what you found, how to reproduce it (a minimal request or document
helps), the Folio version (`/healthz` reports it) and the impact you expect.
We acknowledge reports within five working days, keep you informed while we
fix the problem, and credit you in the release notes unless you prefer
otherwise.

## Supported versions

Security fixes go into the latest release and the default branch. Images
are published for every commit (`sha-<commit>`) and release, so upgrading
is a redeploy.

## Threat model

Folio is a single-tenant service for a team of trusted token holders that
renders untrusted content.

- **Who is trusted.** Every holder of a configured token has full read and
  write access to everything on the instance (brands, templates, documents,
  assets). There are no roles. A leaked token gives that access to whoever
  holds it: rotate it by changing `FOLIO_TOKEN` / `FOLIO_TOKENS` and
  restarting.
- **What is untrusted.** Markdown, metadata, data, template Typst code and
  uploaded assets, even when sent by a token holder: they may come from
  emails, web pages or client files, and template code is written by agents.
  Untrusted content must not read server files, reach the network, or
  exhaust the host.
- **Controls.** A restricted Markdown dialect (raw HTML, TeX and Typst are
  inert); components expanded in the document tree so data cannot inject
  markup; images only from sanitised uploads (SVGs with scripts or external
  references are rejected; URL uploads refuse private, loopback, link-local
  and metadata addresses, checked on the connected IP); template imports
  limited to base layouts and vendored packages; pandoc and typst run as an
  unprivileged user under rlimits (memory, CPU, file size, processes), a
  wall-clock timeout, no network, and Landlock when the kernel supports it;
  size, nesting, table, page and queue limits.
- **Download links.** Files are served without authentication from
  `/f/<random id>/<name>`; the 122-bit random id is a bearer capability.
  Anyone who obtains a link can download that file until the document is
  trashed or purged (template previews expire). Links are not indexable and
  not sent in `Referer` headers.
- **AI reviewer.** When `FOLIO_OPENROUTER_API_KEY` is set, page images of
  the reviewed document (and of template samples on template creation or
  update) are sent to OpenRouter, with provider data collection denied.
  Findings are advisory and never change a document. Without the key no
  content leaves the instance.
- **Logs and audit.** Logs never contain tokens, document content or
  download links. Every mutating MCP call is recorded in the audit table
  with the token name.
- **Out of scope.** Denial of service by a token holder beyond the
  configured limits, the security of the host or platform, and clients that
  leak their own tokens or links.

The container runs Folio as uid 47100. When started as root (needed on
platforms whose volumes are root-owned), the entrypoint only fixes the
ownership of the data directory, without following symlinks, and then drops
to that user with no capabilities and `no_new_privs`.
