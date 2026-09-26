# Private Podcast Platform Specification

This is the product specification the implementation follows.

## 1. System overview

The Private Podcast Platform provides an automated pipeline that allows autonomous agents or scripts to convert text-based updates into a private, password-protected audio podcast feed.

Key goals:

- **Agent integration** — MCP and REST endpoints for seamless agent interaction.
- **Asynchronous processing** — offload TTS to background jobs.
- **Secure delivery** — HTTP authentication supported by major podcast clients.
- **Low operational overhead** — serverless GCP components and lightweight OSS models.

## 2. Functional requirements

### 2.1 Content ingestion (agent interface)

- REST: secure `POST` accepting JSON with title, text, voice preference, and metadata.
- MCP: tool `publish_agent_update` so agents (Claude, custom frameworks) can invoke episode creation.
- Validation: minimum length, UTF-8, maximum size.

### 2.2 Processing and audio synthesis

- Automatically trigger an isolated batch process on successful ingest.
- Convert scripts to high-quality `.mp3` using an embedded OSS TTS engine (Piper ONNX).
- Store generated audio in object storage with structured pathing (`audio/<id>.mp3`).

### 2.3 Podcast distribution (client interface)

- RSS 2.0 feed with `<enclosure>` media tags and iTunes extensions.
- HTTP Basic Auth or token query parameters on both the XML feed and audio URLs.
- Compatible with aggregators that accept credentials in the feed URL (`https://user:password@domain/podcast.xml`).

## 3. Components

See [architecture.md](architecture.md) for the split-plane diagram.

| Component | Runtime | Hosting |
| --- | --- | --- |
| Control plane | Go 1.22+ | Cloud Run Service (or local process) |
| Data plane / worker | Go + Piper + ffmpeg | Cloud Run Job |
| Metadata | Firestore or SQLite | GCP / local file |
| Assets | GCS or local directory | private bucket / `data/` |

## 4. Episode lifecycle

`PENDING` (API: `QUEUED`) → `PROCESSING` → `READY` | `FAILED`.

Only `READY` episodes are rendered into RSS.

## 5. Non-goals

- Public directory listing or Apple Podcasts Connect publishing.
- Multi-tenant account management (one deployment = one show).
- GPU inference; CPU ONNX is the supported path.
- Live / streaming audio. Episodes are finite files.
