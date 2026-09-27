# Podcaster

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](go.mod)

Private podcasts for agents. An agent publishes text over **REST** or **MCP**; a worker turns it into speech with [Piper](https://github.com/rhasspy/piper); each listener gets their own authenticated RSS feed in a normal podcast app.

```
  AI agent (REST / MCP)
           |
           v
  +--------------------+     RunJob      +------------------+
  | Control plane      | ---------------> | Worker (Piper)   |
  | cmd/server         |                 | cmd/worker        |
  | REST, MCP, RSS     |                 +--------+---------+
  +--------+-----------+                          |
           |                                      | MP3
           v                                      v
  SQLite / Firestore                    local disk / private GCS
           ^                                      |
           |                                      v
           +-------- GET /p/{id}/podcast.xml -----+
                    (Basic auth or ?token=)
                              |
                    Apple Podcasts / Overcast / ...
```

There is **one publisher** (the `AGENT_API_KEY`) and **many private shows**. Creating a user means creating a show: slug, title, unique password, feed at `/p/{id}/podcast.xml`. The default `/podcast.xml` is a separate show that uses `FEED_USERNAME` / `FEED_PASSWORD`.

---

## Table of contents

- [Quick start (local)](#quick-start-local)
- [Create a private show](#create-a-private-show)
- [Subscribe in a podcast app](#subscribe-in-a-podcast-app)
- [Agent / MCP](#agent--mcp)
- [Configuration](#configuration)
- [Production (GCP)](#production-gcp)
- [Development commands](#development-commands)
- [Documentation](#documentation)
- [License](#license)

---

## Quick start (local)

Requires **Go 1.26** (see `go.mod`) and optionally **ffmpeg** (needed for Piper, not for the mock voice).

```bash
git clone https://github.com/tvaroska/podcaster.git
cd podcaster
make test
make run
```

`make run` starts the server with SQLite, local disk, an in-process queue, and mock TTS. It does **not** read a `.env` file — it sets those defaults in the process environment.

In another terminal:

```bash
curl -sS -X POST http://localhost:8080/v1/episodes \
  -H "Authorization: Bearer dev-agent-key" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Morning Briefing",
    "content": "Good morning. Here are your top updates for today.",
    "category": "Daily Briefing"
  }'

# wait a second, then:
curl -sS -u podcast:podcast http://localhost:8080/podcast.xml
```

Docker (same defaults, data in the `podcaster-data` volume):

```bash
docker compose up --build
```

End-to-end against a running server:

```bash
make smoke
```

---

## Create a private show

The agent is the publisher. A listener never signs up — you create their show and send them the subscribe URL.

```bash
curl -sS -X POST http://localhost:8080/v1/podcasts \
  -H "Authorization: Bearer dev-agent-key" \
  -H "Content-Type: application/json" \
  -d '{"id":"alice","title":"Alice Briefing","description":"Private updates for Alice"}'
```

Response includes `username`, `password`, `token`, `feed_url`, and `subscribe_url`. Store the password; it is also in the metadata store (SQLite / Firestore) and can be read again with `GET /v1/podcasts/alice`.

Publish only to that feed:

```bash
curl -sS -X POST http://localhost:8080/v1/podcasts/alice/episodes \
  -H "Authorization: Bearer dev-agent-key" \
  -H "Content-Type: application/json" \
  -d '{"title":"Morning","content":"Good morning Alice. Here is your briefing."}'
```

Alice's app uses `http://alice:PASSWORD@localhost:8080/p/alice/podcast.xml`. Bob cannot read it. Episodes with a `podcast_id` never appear on the default `/podcast.xml`.

Full schemas: [docs/api.md](docs/api.md).

---

## Subscribe in a podcast app

Private feeds need credentials. Use the **subscribe URL** from show creation (embedded Basic auth).

```
https://alice:PASSWORD@host/p/alice/podcast.xml
```

Default show (env `FEED_*`):

```
https://FEED_USERNAME:FEED_PASSWORD@host/podcast.xml
```

Locally that is `http://podcast:podcast@localhost:8080/podcast.xml`.

Players that reject `user:pass@host` can use `?token=` instead (`FEED_TOKEN` on the default show, or the show's `token` on `/p/{id}/...`). Enclosure URLs in the RSS already include that token so clients that do not replay Basic auth on media still play. **Sharing one episode link leaks that show's token.**

- **Apple Podcasts**: Library → `…` → Follow a Show by URL
- **Overcast**: `+` → Add URL
- **Pocket Casts**: Search or enter URL

Channel includes `<itunes:block>yes</itunes:block>` so public directories should not index it.

---

## Agent / MCP

Streamable HTTP at `/mcp`, same Bearer token as REST.

Claude Desktop / Claude Code (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "podcaster": {
      "url": "http://localhost:8080/mcp",
      "headers": {
        "Authorization": "Bearer dev-agent-key"
      }
    }
  }
}
```

In production, use `https://YOUR_SERVICE/mcp` and the real `AGENT_API_KEY`.

| Tool | What |
| --- | --- |
| `create_podcast` | Create a private show. Returns username, password, token, feed URL. |
| `publish_agent_update` | Queue an episode. Optional `podcast_id` (empty = default feed). |
| `get_episode_status` | `QUEUED` → `PROCESSING` → `READY` or `FAILED`. |

REST equivalents: `POST /v1/podcasts`, `POST /v1/podcasts/{id}/episodes`, `GET /v1/episodes/{id}`.

---

## Configuration

All settings are **process environment variables**. The binary does not load `.env`. `.env.example` is a checklist. To use a file locally:

```bash
set -a && source .env && set +a
make build
./bin/server
```

| Variable | Local default | Notes |
| --- | --- | --- |
| `AGENT_API_KEY` | `dev-agent-key` | Bearer for REST + MCP |
| `FEED_USERNAME` / `FEED_PASSWORD` | `podcast` / `podcast` | Default show only |
| `FEED_TOKEN` | `dev-feed-token` | Default show `?token=` |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | Absolute links in RSS. Must be the URL listeners use. |
| `STORE_BACKEND` | `sqlite` | `firestore` in GCP |
| `STORAGE_BACKEND` | `local` | `gcs` in GCP |
| `JOB_BACKEND` | `local` | `cloudrun` on the **service** only |
| `TTS_ENGINE` | `mock` | `piper` on the **worker** image |
| `CLOUD_RUN_JOB_NAME` | — | Job name. Do not set `CLOUD_RUN_JOB` on a Cloud Run Service (reserved). |
| `GCP_PROJECT` / `GCS_BUCKET` | — | Required for Firestore / GCS |

`Validate()` only checks that secrets are **non-empty**. `dev-agent-key` / `podcast` are accepted even with Firestore and GCS. Generate real secrets for production (the bootstrap script does).

Full table: [docs/deployment.md](docs/deployment.md).

---

## Production (GCP)

Do not start from `deploy/cloudrun-*.yaml` — those files are comments plus placeholders. Follow **[docs/deployment.md](docs/deployment.md)** in order:

1. Existing GCP project with **billing** and a principal that can grant IAM (typically Owner).
2. `./deploy/bootstrap-gcp.sh` — APIs, Artifact Registry, Firestore + indexes, bucket, runtime SA, secrets, Cloud Build IAM.
3. `gcloud builds submit` — server (distroless) and worker (Debian + Piper + ffmpeg).
4. Deploy the **Job** first, then the **Service**.
5. Pin `PUBLIC_BASE_URL` to the service URL you will actually subscribe with.
6. Create a show, publish an episode, subscribe.

If `--allow-unauthenticated` fails with `iam.allowedPolicyMemberDomains`, use `--no-invoker-iam-check` (documented in the runbook). Public `/healthz` may then be Google-frontend HTML; `/readyz` is the app probe.

---

## Development commands

```bash
make build        # bin/server and bin/worker
make test
make vet
make fmt
make tidy
make run          # local defaults, mock TTS
make smoke        # ingest + RSS against localhost:8080
make docker-up
make docker-down
make clean
```

---

## Documentation

- [docs/deployment.md](docs/deployment.md) — GCP from an empty project (the runbook)
- [docs/api.md](docs/api.md) — REST, RSS, MCP
- [docs/architecture.md](docs/architecture.md) — control/data plane, CAS, storage
- [docs/spec.md](docs/spec.md) — scope and non-goals

---

## License

Apache 2.0. See [LICENSE](LICENSE).
