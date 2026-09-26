# Podcaster

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)](go.mod)

**Podcaster** is a private podcast platform tailored for autonomous AI agents and automated workflows. Agents publish textual briefings and updates via **REST** or **MCP (Model Context Protocol)**; background workers synthesize natural speech via [Piper ONNX](https://github.com/rhasspy/piper); and listeners stream the generated episodes through an authenticated, private RSS feed in any standard podcast player.

```
                           +---------------------------+
                           |  AI Agent (Claude / Code) |
                           +---------------------------+
                                         |
                                (REST / MCP Requests)
                                         v
+----------------------------------------------------------------------------------+
| Control Plane (`cmd/server`)                                                     |
|  - Ingest: POST /v1/episodes               - MCP Server: /mcp                    |
|  - Feed: GET /podcast.xml                  - Audio Stream: GET /audio/{id}.mp3   |
+----------------------------------------------------------------------------------+
          |                         |                                  ^
  1. Save Metadata          2. Enqueue Job                     4. Audio Delivery
          v                         v                                  |
  +---------------+        +------------------+                        |
  | SQLite /      |        | Cloud Run Job /  |                        |
  | Firestore     |        | In-Process Queue |                        |
  +---------------+        +------------------+                        |
          ^                         |                                  |
          | 3. Update Status        v                                  |
          +----------------- +--------------+                          |
                             | TTS Worker   | --- Upload MP3 ---> +---------------+
                             | (cmd/worker) |                     | Local Disk /  |
                             +--------------+                     | Private GCS   |
                                                                  +---------------+
                                                                       ^
                                                                       |
                                                      +-------------------------------+
                                                      | Podcast App                   |
                                                      | (Apple Podcasts, Overcast...) |
                                                      +-------------------------------+
```

---

## Highlights

- **🤖 Native Agent Integration**: First-class support for both standard **REST JSON APIs** and **Model Context Protocol (MCP)** streamable HTTP endpoints (`/mcp`), enabling Claude Desktop, Claude Code, Cursor, or custom agents to publish updates natively.
- **⚡ Asynchronous Audio Synthesis**: Non-blocking ingestion. TTS runs out-of-band via embedded CPU-optimized [Piper ONNX](https://github.com/rhasspy/piper) (or a zero-dependency mock synthesizer for dev).
- **🔒 Private & Secure Feed**: Access-controlled via HTTP Basic Authentication or signed query tokens. Channel includes `<itunes:block>yes</itunes:block>` to prevent indexing by public podcast directories.
- **📱 Universal Podcast Player Compatibility**: Seamlessly works with Apple Podcasts, Overcast, Pocket Casts, Castro, and AntennaPod. Enclosure media links preserve token authentication automatically.
- **☁️ Serverless or Self-Hosted**: Runs as a single lightweight binary locally (SQLite + local disk + in-process queue) or scales serverlessly on GCP (Cloud Run Services + Cloud Run Jobs + Firestore + GCS).

---

## Table of Contents

- [Quick Start](#quick-start)
  - [Prerequisites](#prerequisites)
  - [Run Locally (Native Go)](#run-locally-native-go)
  - [Run with Docker Compose](#run-with-docker-compose)
  - [Automated Smoke Test](#automated-smoke-test)
- [Subscribing in Podcast Apps](#subscribing-in-podcast-apps)
- [Agent & MCP Integration](#agent--mcp-integration)
  - [REST API Usage](#rest-api-usage)
  - [Claude Desktop & Claude Code Configuration](#claude-desktop--claude-code-configuration)
  - [Available MCP Tools](#available-mcp-tools)
- [Pluggable Architecture](#pluggable-architecture)
- [Configuration Reference](#configuration-reference)
- [Production Deployment (GCP)](#production-deployment-gcp)
- [Development Commands](#development-commands)
- [Documentation Index](#documentation-index)
- [License](#license)

---

## Quick Start

### Prerequisites

- **Go 1.22+** (Go 1.24 recommended)
- **ffmpeg** (optional for mock TTS; required when using Piper ONNX)
- **Docker** (optional, for containerized run)

### Run Locally (Native Go)

Clone the repository and run the server with local development defaults:

```bash
git clone git@github.com:tvaroska/podcaster.git
cd podcaster

# Run test suite
make test

# Start the server (SQLite + local storage + in-process mock TTS)
make run
```

In another terminal, publish a test episode:

```bash
# 1. Post a new episode (authenticated with the dev bearer token)
curl -sS -X POST http://localhost:8080/v1/episodes \
  -H "Authorization: Bearer dev-agent-key" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Morning Briefing",
    "content": "Good morning. Here are your top updates for today.",
    "category": "Daily Briefing"
  }'

# 2. Wait a couple seconds for synthesis, then pull the authenticated RSS feed:
curl -sS -u podcast:podcast http://localhost:8080/podcast.xml
```

### Run with Docker Compose

A containerized environment with persistent data storage is preconfigured:

```bash
docker compose up --build
```

The server binds to `http://localhost:8080` with volume-backed persistence in `podcaster-data`.

### Automated Smoke Test

Run the built-in end-to-end smoke test which posts an episode, polls until `READY`, and fetches the generated RSS feed:

```bash
make smoke
```

---

## Subscribing in Podcast Apps

Podcaster generates an RSS 2.0 feed with full iTunes podcast tags. Because the feed is private, clients must authenticate.

### Supported Subscription Formats

1. **Embedded Basic Authentication URL** (recommended for Overcast, Pocket Casts, Apple Podcasts):
   ```
   http://podcast:podcast@localhost:8080/podcast.xml
   ```
   *(In production, replace with your public HTTPS address, e.g. `https://username:password@podcast.example.com/podcast.xml`)*

2. **Query Parameter Token** (for players that do not support URL-embedded credentials):
   ```
   http://localhost:8080/podcast.xml?token=dev-feed-token
   ```

> **Note on Media Enclosures**: Some podcast clients do not pass Basic Auth headers when following media redirects. Podcaster automatically attaches `?token=...` to all enclosure and audio URLs within the RSS feed, guaranteeing uninterrupted playback.

### App Setup Instructions

- **Apple Podcasts**: Go to *Library* → Tap `…` (menu) → *Follow a Show by URL* → Paste the authenticated feed URL.
- **Overcast**: Tap `+` → *Add URL* → Paste the authenticated feed URL.
- **Pocket Casts**: Paste the URL directly into the *Search or enter URL* box in the Podcasts tab.

---

## Agent & MCP Integration

### REST API Usage

#### Ingest Episode (`POST /v1/episodes`)

```bash
curl -sS -X POST http://localhost:8080/v1/episodes \
  -H "Authorization: Bearer dev-agent-key" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Autonomous Daily Briefing",
    "content": "All systems operating normally. Database backup completed at 04:00 UTC.",
    "category": "Ops",
    "voice_id": "en_US-lessac-medium"
  }'
```

**Response (`202 Accepted`):**
```json
{
  "episode_id": "ep_01ja2b3c4d5e6f",
  "status": "QUEUED",
  "created_at": "2026-09-26T13:30:00Z"
}
```

#### Poll Status (`GET /v1/episodes/{id}`)

```bash
curl -sS http://localhost:8080/v1/episodes/ep_01ja2b3c4d5e6f \
  -H "Authorization: Bearer dev-agent-key"
```

**Response (`200 OK`):**
```json
{
  "episode_id": "ep_01ja2b3c4d5e6f",
  "title": "Autonomous Daily Briefing",
  "status": "READY",
  "category": "Ops",
  "voice_id": "en_US-lessac-medium",
  "duration_seconds": 18.5,
  "file_size_bytes": 296320,
  "created_at": "2026-09-26T13:30:00Z",
  "published_at": "2026-09-26T13:30:04Z"
}
```

Status transitions: `QUEUED` → `PROCESSING` → `READY` (or `FAILED`).

---

### Claude Desktop & Claude Code Configuration

Podcaster implements the standard **Model Context Protocol (MCP)** over Streamable HTTP at `/mcp`.

Add this server configuration to your `claude_desktop_config.json`:

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

For production deployments over HTTPS:

```json
{
  "mcpServers": {
    "podcaster": {
      "url": "https://podcast.example.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_AGENT_API_KEY"
      }
    }
  }
}
```

### Available MCP Tools

Agents connected via MCP have access to the following tools:

| Tool Name | Parameters | Description |
| --- | --- | --- |
| `publish_agent_update` | `title` *(string, required)*<br>`content` *(string, required)*<br>`category` *(string, optional)*<br>`voice_id` *(string, optional)* | Submits a new text update to be synthesized and queued for the podcast feed. Returns the assigned `episode_id`. |
| `get_episode_status` | `episode_id` *(string, required)* | Retrieves the current synthesis status (`QUEUED`, `PROCESSING`, `READY`, `FAILED`), duration, and timestamps. |

---

## Pluggable Architecture

Podcaster separates the control plane and data plane, allowing components to be swapped cleanly between local development and production GCP environments:

| Component | Local Dev (`default`) | Production GCP |
| --- | --- | --- |
| **Control Plane** | `cmd/server` (local HTTP) | Cloud Run Service |
| **Worker / Data Plane** | In-process goroutine / local worker | Cloud Run Job (ephemeral execution) |
| **Metadata Store** | SQLite (`data/podcaster.db`) | Google Cloud Firestore |
| **Object Storage** | Local directory (`data/`) | Google Cloud Storage (GCS) |
| **TTS Engine** | Mock tone generator (zero dependency) | Piper ONNX (fast neural CPU synthesis) |

---

## Configuration Reference

Configure Podcaster via environment variables or a `.env` file (see `.env.example`).

### Server & Network

| Variable | Default | Description |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | Bind address (if unset, Cloud Run `PORT` is used) |
| `PUBLIC_BASE_URL` | `http://localhost:8080` | Origin URL used in RSS enclosures and artwork links |
| `SHUTDOWN_TIMEOUT`| `25s` | Graceful HTTP shutdown window |

### Authentication & Secrets

| Variable | Default (Dev Mode) | Description |
| --- | --- | --- |
| `AGENT_API_KEY` | `dev-agent-key` | Bearer token required for REST ingest & MCP tools |
| `FEED_USERNAME` | `podcast` | HTTP Basic Auth username for feed & audio |
| `FEED_PASSWORD` | `podcast` | HTTP Basic Auth password for feed & audio |
| `FEED_TOKEN` | `dev-feed-token` | Query token fallback for feed & enclosure requests |
| `PODCASTER_DEV` | unset | Set to `1` to allow development defaults |

> ⚠️ **Security Warning**: Local development default credentials (`dev-agent-key`, `podcast`/`podcast`) are rejected in production backends (`firestore`, `gcs`, or `cloudrun`). Always set strong secrets in production!

### Backends & TTS

| Variable | Default | Allowed Values / Description |
| --- | --- | --- |
| `STORE_BACKEND` | `sqlite` | `sqlite` or `firestore` |
| `SQLITE_PATH` | `data/podcaster.db` | File path for SQLite database |
| `STORAGE_BACKEND`| `local` | `local` or `gcs` |
| `LOCAL_DATA_DIR` | `data` | Directory for local audio files |
| `GCS_BUCKET` | *(none)* | GCP bucket name (required when `STORAGE_BACKEND=gcs`) |
| `JOB_BACKEND` | `local` | `local` or `cloudrun` |
| `TTS_ENGINE` | `mock` | `mock` or `piper` |
| `PIPER_BIN` | `piper` | Path to Piper binary |
| `PIPER_MODEL` | *(none)* | Path to `.onnx` voice model (required for Piper) |
| `PIPER_CONFIG` | *(none)* | Path to `.onnx.json` model config file |
| `FFMPEG_BIN` | `ffmpeg` | Path to ffmpeg binary |
| `DEFAULT_VOICE` | `en_US-lessac-medium` | Default voice identifier |

### Podcast Metadata

| Variable | Default | Description |
| --- | --- | --- |
| `PODCAST_TITLE` | `Private Agent Briefing` | Feed channel title |
| `PODCAST_DESCRIPTION` | *(standard description)* | Feed channel description |
| `PODCAST_AUTHOR` | `Podcaster` | `itunes:author` |
| `PODCAST_LANGUAGE` | `en-us` | RSS `<language>` code |
| `PODCAST_CATEGORY` | `Technology` | Primary `itunes:category` |
| `PODCAST_IMAGE_FILE` | *(none)* | Custom 1400x1400 PNG path (generated if omitted) |

---

## Production Deployment (GCP)

Podcaster is designed to run serverlessly on Google Cloud Platform:

1. **Cloud Run Service (`cmd/server`)**: Handles REST ingest, MCP connections, and feeds the protected RSS/MP3 streams.
2. **Cloud Run Job (`cmd/worker`)**: Triggered per episode to execute Piper TTS + ffmpeg in an isolated, autoscaling task container.
3. **Firestore**: Persists episode states and metadata with atomic conditional updates.
4. **Cloud Storage**: Secure private bucket hosting synthesized MP3 enclosures.
5. **Secret Manager**: Securely mounts `AGENT_API_KEY`, `FEED_PASSWORD`, and other credentials.

Deployment descriptors and configurations are located in `deploy/`:
- `deploy/cloudbuild.yaml` — Multi-target image build
- `deploy/cloudrun-service.yaml` — Control plane service definition
- `deploy/cloudrun-job.yaml` — Audio synthesis worker job
- `deploy/firestore.indexes.json` — Composite Firestore indexes

See [docs/deployment.md](docs/deployment.md) for step-by-step setup and IAM permission details.

---

## Development Commands

All common tasks are encapsulated in the `Makefile`:

```bash
make build       # Compile server and worker binaries into bin/
make test        # Run unit and integration tests
make vet         # Run go vet static analysis
make fmt         # Format source code
make tidy        # Clean up go.mod and go.sum dependencies
make run         # Build and launch server locally with mock backends
make smoke       # Run end-to-end ingestion and RSS smoke test
make docker-up   # Start local instance via docker compose
make docker-down # Stop docker compose services
make clean       # Remove built binaries
```

---

## Documentation Index

- [Product Specification](docs/spec.md) — Functional requirements and design scope
- [Architecture Details](docs/architecture.md) — Split-plane topology, storage schema, and CAS lifecycle
- [API Reference](docs/api.md) — Full REST schemas, error codes, and MCP tool protocols
- [Deployment Guide](docs/deployment.md) — Production GCP infrastructure and environment setup

---

## License

This project is licensed under the Apache 2.0 License. See the [LICENSE](LICENSE) file for details.
