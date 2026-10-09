# Podcaster

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](go.mod)

Private podcasts for agents. An agent publishes text over **REST** or **MCP**; a worker turns it into speech with [Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M) (via [`sherpa-onnx`](https://github.com/k2-fsa/sherpa-onnx), or [Piper](https://github.com/rhasspy/piper)); each listener gets their own authenticated RSS feed in a normal podcast app.

```
  AI agent (REST / MCP)
           |
           v
  +--------------------+     RunJob      +------------------+
  | Control plane      | ---------------> | Worker (Kokoro)  |
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

Credentials are separated into three roles: a **Super Key** (`ADMIN_API_KEY`, or `AGENT_API_KEY` when `ADMIN_API_KEY` is unset) to create and manage shows, a **Per-User Submit Key** (`submit_key`) scoped to publishing to a single show, and a **Per-User Listening Key** (`username`/`password` and `?token=`) for the podcast app at `/p/{id}/podcast.xml`. The default `/podcast.xml` is a separate show that uses `AGENT_API_KEY` for publishing and `FEED_USERNAME` / `FEED_PASSWORD` / `FEED_TOKEN` for listening.

---

## Table of contents

- [Quick start (local)](#quick-start-local)
- [Create a private show](#create-a-private-show)
- [Subscribe in a podcast app](#subscribe-in-a-podcast-app)
- [Agent / MCP](#agent--mcp)
- [Voices & audio pipeline](#voices--audio-pipeline)
- [Configuration](#configuration)
- [Production (GCP)](#production-gcp)
- [Development commands](#development-commands)
- [Documentation](#documentation)
- [License](#license)

---

## Quick start (local)

Requires **Go 1.26** (see `go.mod`) and optionally **ffmpeg** (needed for Kokoro/Piper, not for the mock voice).

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

Creating a show requires the **Super Key** (`ADMIN_API_KEY`, or `AGENT_API_KEY` when `ADMIN_API_KEY` is unset). Each show returns two separate sets of credentials:

- **Per-User Submit Key (`submit_key`)**: A Bearer token scoped to publishing and updating episodes (`PATCH /v1/episodes/{ep}`), checking episode status, viewing (`GET /v1/podcasts/{id}`) and updating (`PATCH /v1/podcasts/{id}`) the show's metadata, and rotating listening credentials for that show only. It cannot create or list other shows (`403`) and cannot read the RSS feed or MP3s.
- **Per-User Listening Key (`username`, `password`, `token`, `subscribe_url`)**: Used by the listener's podcast app to fetch `/p/{id}/podcast.xml`, `/p/{id}/audio/*`, and `/p/{id}/episodes/{ep}/chapters.json`.

```bash
curl -sS -X POST http://localhost:8080/v1/podcasts \
  -H "Authorization: Bearer dev-agent-key" \
  -H "Content-Type: application/json" \
  -d '{"id":"alice","title":"Alice Briefing","description":"Private updates for Alice","image_url":"https://example.com/alice-cover.png"}'
```

Response includes `submit_key`, `username`, `password`, `token`, `feed_url`, and `subscribe_url`. Store the credentials; they are also in the metadata store (SQLite / Firestore) and can be retrieved again with `GET /v1/podcasts/alice`. You can update a show's `title`, `description`, `author`, or `image_url` at any time via `PATCH /v1/podcasts/alice` (or MCP `update_podcast`).

Publish only to that feed (using the show's `submit_key` or the Super Key), optionally including episode show notes (`description`), per-episode artwork (`image_url`), and chapter markers (`chapters`):

```bash
curl -sS -X POST http://localhost:8080/v1/podcasts/alice/episodes \
  -H "Authorization: Bearer $ALICE_SUBMIT_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Morning Briefing",
    "content": "Good morning Alice. Here is your briefing.",
    "description": "Show notes and key links for today.",
    "image_url": "https://example.com/episodes/morning.png",
    "chapters": [{"start_seconds": 0, "title": "Intro"}, {"start_seconds": 15, "title": "Top Stories"}]
  }'
```

Update an existing episode's `title`, `description`, `category`, `image_url`, or `chapters` without re-synthesizing audio via `PATCH /v1/episodes/{id}` (or MCP `update_episode`).

Rotate Alice's listening credentials (`password` and `token`) if a feed or episode link leaks:

```bash
curl -sS -X POST http://localhost:8080/v1/podcasts/alice/rotate \
  -H "Authorization: Bearer $ALICE_SUBMIT_KEY" \
  -H "Content-Type: application/json" \
  -d '{}'
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

Players that reject `user:pass@host` can use `?token=` instead (`FEED_TOKEN` on the default show, or the show's `token` on `/p/{id}/...`). Enclosure URLs in the RSS already include that token so clients that do not replay Basic auth on media still play. **Sharing one episode link leaks that show's token** — rotate leaked listening credentials via `POST /v1/podcasts/{id}/rotate` or the `rotate_podcast_credentials` MCP tool.

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

In production, use `https://YOUR_SERVICE/mcp` and the real `ADMIN_API_KEY`, `AGENT_API_KEY`, or per-show `submit_key`.

| Tool | What |
| --- | --- |
| `create_podcast` | Create a private show (requires Super Key), with optional `description`, `author`, and custom `image_url`. Returns `submit_key`, `username`, `password`, `token`, `feed_url`, `subscribe_url`. |
| `update_podcast` | Update a show's `title`, `description`, `author`, and/or `image_url` (callable with Super Key or that show's `submit_key`). |
| `list_podcasts` | List all private shows and their credentials/subscribe URLs (requires Super Key). |
| `get_podcast` | Retrieve metadata, credentials (`submit_key`, `password`, `token`), and subscribe URL for a private show by slug. |
| `rotate_podcast_credentials` | Rotate a show's listener `password`/`token` (default) and/or `submit_key`, returning the new `subscribe_url`. |
| `publish_agent_update` | Queue an episode with optional `description` (show notes), `image_url` (episode icon), `chapters`, and `podcast_id` (empty = default feed, or the scoped show when using a `submit_key`). |
| `update_episode` | Update an existing episode's `title`, `description`, `category`, `image_url`, and/or `chapters`. |
| `get_episode_status` | `QUEUED` → `PROCESSING` → `READY` or `FAILED` (plus `description`, `image_url`, and `chapters`). |
| `list_episodes` | List recent episodes with optional `status`, `podcast_id`, `only_default`, `limit`, and `offset` filters. |

REST equivalents: `POST /v1/podcasts`, `PATCH /v1/podcasts/{id}`, `GET /v1/podcasts`, `GET /v1/podcasts/{id}`, `POST /v1/podcasts/{id}/rotate`, `POST /v1/podcasts/{id}/episodes`, `PATCH /v1/episodes/{id}`, `GET /v1/episodes/{id}`, `GET /v1/episodes`.

---

## Voices & audio pipeline

The production worker image bundles **[Kokoro-82M v1.0](https://huggingface.co/hexgrad/Kokoro-82M)** (`kokoro-multi-lang-v1_0`) via [`sherpa-onnx`](https://github.com/k2-fsa/sherpa-onnx) (`TTS_ENGINE=kokoro`, `DEFAULT_VOICE=af_heart`, `KOKORO_SPEED=0.95`). By default, `af_heart` blends `0.7 * af_heart + 0.3 * af_bella` for warmer narration (custom blends like `af_heart:0.7,af_bella:0.3` or `0.7*af_heart+0.3*af_bella` are also supported). Before synthesis, the worker strips SSML and Markdown formatting, spaces initialisms (`ILRS` → `I L R S`), applies phonetic overrides (`Chang'e` → `Chahng-uh`, `Lavochkin` → `Lah-votch-keen`), adds structural pause cues, splits the script along abbreviation-aware sentence and paragraph boundaries (`<= 500` runes per chunk), synthesizes each chunk at 24 kHz with 8 ms linear boundary fades plus 300 ms sentence / 700 ms paragraph silence, and masters the concatenated audio with `ffmpeg` (`highpass=f=80`, 6–8 kHz de-essing EQ, 2:1 downward compression `30ms/100ms`, `loudnorm=I=-19:TP=-1.0:LRA=11`, 128 kbps 24 kHz mono MP3).

Built-in English `voice_id` values in `kokoro-multi-lang-v1_0`:

| Accent / gender | `voice_id` options |
| --- | --- |
| US English — female | `af_heart` *(default)*, `af_alloy`, `af_aoede`, `af_bella`, `af_jessica`, `af_kore`, `af_nicole`, `af_nova`, `af_river`, `af_sarah`, `af_sky` |
| US English — male | `am_adam`, `am_echo`, `am_eric`, `am_fenrir`, `am_liam`, `am_michael`, `am_onyx`, `am_puck`, `am_santa` |
| UK English — female | `bf_alice`, `bf_emma`, `bf_isabella`, `bf_lily` |
| UK English — male | `bm_daniel`, `bm_fable`, `bm_george`, `bm_lewis` |

Set `VOICE_ALLOWLIST` on the control plane to restrict which `voice_id` values callers may request.

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
| `ADMIN_API_KEY` | — | Optional super key for creating/listing shows (`POST/GET /v1/podcasts`). When unset, `AGENT_API_KEY` acts as the super key. |
| `AGENT_API_KEY` | `dev-agent-key` | Bearer for REST + MCP (default-feed submit key when `ADMIN_API_KEY` is set; super key otherwise) |
| `FEED_USERNAME` / `FEED_PASSWORD` | `podcast` / `podcast` | Default show only |
| `FEED_TOKEN` | `dev-feed-token` | Default show `?token=` |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | Absolute links in RSS. Must be the URL listeners use. |
| `STORE_BACKEND` | `sqlite` | `firestore` in GCP |
| `STORAGE_BACKEND` | `local` | `gcs` in GCP |
| `JOB_BACKEND` | `local` | `cloudrun` on the **service** only |
| `TTS_ENGINE` | `mock` | `kokoro` (or `piper`) on the **worker** image |
| `KOKORO_THREADS` | `2` | CPU threads per `sherpa-onnx-offline-tts` process |
| `KOKORO_CONCURRENCY` | `1` | Parallel Kokoro chunk processes (`<= 1` runs sequentially). Size so `KOKORO_CONCURRENCY × KOKORO_THREADS ≤ vCPU`. |
| `DEFAULT_VOICE` | `af_heart` | Default narrator (`af_heart`, `af_bella`, `am_adam`, `am_fenrir`, `am_michael`, `bf_emma`, `bm_george`, …) |
| `CLOUD_RUN_JOB_NAME` | — | Job name. Do not set `CLOUD_RUN_JOB` on a Cloud Run Service (reserved). |
| `GCP_PROJECT` / `GCS_BUCKET` | — | Required for Firestore / GCS |

`Validate()` only checks that secrets are **non-empty**. `dev-agent-key` / `podcast` are accepted even with Firestore and GCS. Generate real secrets for production (the bootstrap script does).

Full table: [docs/deployment.md](docs/deployment.md).

---

## Production (GCP)

Do not start from `deploy/cloudrun-*.yaml` — those files are comments plus placeholders. Follow **[docs/deployment.md](docs/deployment.md)** in order:

1. Existing GCP project with **billing** and a principal that can grant IAM (typically Owner).
2. `./deploy/bootstrap-gcp.sh` — APIs, Artifact Registry, Firestore + indexes, bucket, runtime SA, secrets, Cloud Build IAM.
3. `gcloud builds submit` — server (distroless) and worker (Debian + Kokoro-82M / `sherpa-onnx` + ffmpeg).
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
