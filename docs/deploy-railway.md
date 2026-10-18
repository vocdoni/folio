# Deploying Folio on Railway

Folio runs on [Railway](https://railway.com) as one service built from the
published image, with one volume for its data. Railway does not run replicas
of a service that has a volume, which matches Folio's single-instance
design.

The examples use `folio.example.com` as the public domain and
`ghcr.io/vocdoni/folio` as the image; use your own domain (and your own image
if you publish a fork).

## 1. Create the service

In a Railway project: **New → Docker Image** and enter
`ghcr.io/vocdoni/folio:latest`, or pin a build with `ghcr.io/vocdoni/folio:sha-<commit>`
or a release tag such as `ghcr.io/vocdoni/folio:0.1.0`.

With the CLI (`railway login` and `railway link` to the project first):

```sh
railway add --service folio --image ghcr.io/vocdoni/folio:latest
```

`latest` follows the default branch, and it moves only after the image passed
the smoke tests natively on amd64 and arm64: the publish workflow pushes the
immutable `sha-<commit>` tag first, and a separate job adds `latest` once both
smoke tests are green. With Railway's image auto-update enabled on `latest`,
every tested merge is deployed. A pinned `sha-<commit>` tag changes only when
you change it, which makes deploys and rollbacks explicit.

## 2. Attach a volume at `/data`

**Service → Settings → Volumes → Add volume**, mount path `/data`, or:

```sh
railway volume --service folio add --mount-path /data
```

Folio keeps its SQLite database, rendered files, assets, backups and runtime
files there. Size it for your documents plus `FOLIO_BACKUP_KEEP` database
snapshots; 1 GB is plenty to start.

## 3. Set the variables

| Variable | Value |
|---|---|
| `FOLIO_PUBLIC_URL` | `https://folio.example.com` (the domain of step 4; download links are built from it) |
| `FOLIO_TOKENS` | `alice:<token>,bob:<token>` (or a single `FOLIO_TOKEN`); generate each token with `openssl rand -hex 32` |
| `RAILWAY_RUN_UID` | `0` |
| `RAILWAY_DEPLOYMENT_DRAINING_SECONDS` | `90` |
| `FOLIO_OPENROUTER_API_KEY` | optional: enables the AI reviewer and the web UI assistants |
| `FOLIO_UI_PASSWORD` | optional: enables the web UI at `/ui` (at least 12 characters); generate it with `openssl rand -base64 24` |
| `FOLIO_RENDER_CONCURRENCY` | optional: about (service memory − 1 GiB) / 1 GiB; the default `2` suits 3 GB or more |

Why the two Railway variables:

- **`RAILWAY_RUN_UID=0`**: Railway volumes are owned by root. The image
  normally runs as the unprivileged `folio` user (uid 47100); with this
  variable the container starts as root, the entrypoint gives `/data` to
  `folio` and then drops to `folio` for good (no capabilities,
  `no_new_privs`) before Folio starts. Folio itself never runs as root.
- **`RAILWAY_DEPLOYMENT_DRAINING_SECONDS=90`**: on a redeploy Railway sends
  SIGTERM and waits this long before killing the old container. Folio stops
  accepting new requests and finishes in-flight renders within
  `FOLIO_RENDER_TIMEOUT` + 20 s (80 s by default). Keep the value at least
  that large if you raise `FOLIO_RENDER_TIMEOUT`.

Folio listens on Railway's `PORT`. With the CLI, set variables without
echoing secrets into your shell history, for example:

```sh
railway variable set --service folio FOLIO_PUBLIC_URL=https://folio.example.com \
  RAILWAY_RUN_UID=0 RAILWAY_DEPLOYMENT_DRAINING_SECONDS=90
printf 'alice:%s' "$(openssl rand -hex 32)" | railway variable set --service folio --stdin FOLIO_TOKENS
```

Keep a copy of the tokens in your password manager: agents need them.

The web UI needs only `FOLIO_UI_PASSWORD`; pipe it in so it never lands in
your shell history:

```sh
openssl rand -base64 24 | tr -d '\n' | railway variable set --service folio --stdin FOLIO_UI_PASSWORD
```

Without the variable `/ui` answers 404. Sessions live in memory, so a deploy
signs everyone out.

## 4. Domain and health check

**Service → Settings → Networking**: add your custom domain
(`folio.example.com`) or generate a Railway domain. For a custom domain,
Railway shows the DNS records to create at your DNS provider (a `CNAME` to
the Railway target, plus a `TXT` record for verification when asked); the
certificate is issued automatically once DNS resolves. With the CLI,
`railway domain folio.example.com` prints the same records.

Set **Settings → Deploy → Healthcheck Path** to `/healthz`. Folio answers it
once the database is open, the tools passed their self-test and the starter
content is seeded (a few seconds on first start).

`FOLIO_PUBLIC_URL` must match the domain clients use, or download links will
point elsewhere.

## 5. Deploy and check

Deploy (Railway deploys after the variables are saved; `railway redeploy`
redeploys the current image). Then:

```sh
curl https://folio.example.com/healthz
# {"status":"ok","version":"sha-…","sandbox":"landlock+rlimits","schema_version":1,"reviewer":false}
curl -s -o /dev/null -w '%{http_code}\n' -X POST https://folio.example.com/mcp   # 401
```

`sandbox` is `landlock+rlimits` when Railway's kernel offers Landlock, or
`rlimits-only` otherwise (rendering still works with rlimits, timeouts and
no network). Connect an agent as shown in the README and render the starter
proposal.

## Backups

- **Railway volume backups**: **Volume → Backups** for manual or scheduled
  backups of the whole volume (database and files). Take one before every
  upgrade that mentions a schema change in the CHANGELOG.
- **Folio snapshots**: Folio writes a consistent database snapshot to
  `/data/backups` every `FOLIO_BACKUP_INTERVAL` (default 24 h) and keeps
  `FOLIO_BACKUP_KEEP` (default 7). They live on the same volume, so they
  protect against mistakes, not against losing the volume.

On Railway, restore a **volume backup** (it brings back the database and the
files together). A Folio snapshot can be downloaded with
`railway volume files download /backups/folio-<time>.db ./folio.db` and
used as the `folio.db` of a fresh data directory (locally or on another
instance) to inspect or recover documents.

## Upgrades and rollback

Every push to the default branch publishes `sha-<commit>`; `latest` and
`main` are added to that digest only after the native smoke tests on both
architectures pass. Releases publish `<version>` tags. To upgrade, point the
service at the new tag (or let auto-update follow `latest`).

Before upgrading across a schema migration (the CHANGELOG says so; migration
0002 adds the web UI activity feed and is the first), take a **volume backup**
(**Volume → Backups**): a migrated database cannot be opened by the previous
binary. To roll back, point the service at the previous tag:

```sh
railway service source connect --service folio --image ghcr.io/vocdoni/folio:sha-<previous>
```

or use **Deployments → Rollback** in the dashboard. A Folio binary refuses to
open a database written by a newer schema version, so after migration 0002
a rollback means restoring the volume backup taken before the upgrade **and**
setting the previous `sha-<commit>` image; rolling back the image alone
leaves a service that will not start. Turn auto-update off while you do this,
or it will redeploy `latest`.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| Startup log `invalid configuration` | a variable is missing or malformed; the log lists each problem |
| `… /data is not writable by uid 47100` | `RAILWAY_RUN_UID=0` is missing |
| Deploy never becomes healthy | check the logs for the tool self-test; use the published image (it ships the right pandoc and typst) |
| Renders fail with `busy` | raise `FOLIO_RENDER_CONCURRENCY` (and the service memory) or `FOLIO_RENDER_QUEUE` |
| Download links point to the wrong host | fix `FOLIO_PUBLIC_URL` |
| `/ui` answers 404 | `FOLIO_UI_PASSWORD` is not set (or shorter than 12 characters: the deploy log says so) |
| The UI signs everyone out after a deploy | expected: sessions live in memory |
| Long renders cut off during deploys | raise `RAILWAY_DEPLOYMENT_DRAINING_SECONDS` to at least `FOLIO_RENDER_TIMEOUT` + 20 s |
