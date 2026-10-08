# Private Podcast Platform Specification

This is the product specification the implementation follows.

## 1. System overview

The Private Podcast Platform converts text posted by an agent into private, password-protected audio podcast feeds.

One **publisher** (the agent, authenticated with `AGENT_API_KEY`) owns the deployment. Each **listener** is a show: slug, unique credentials, feed at `/p/{id}/podcast.xml`, isolated episodes. The default `/podcast.xml` is a separate show configured with `FEED_*`.

Key goals:

- **Agent integration** — MCP and REST, no UI required.
- **Asynchronous processing** — TTS in a background job.
- **Secure delivery** — HTTP Basic / token accepted by major podcast clients.
- **Low operational overhead** — serverless GCP components and Kokoro-82M ONNX on CPU.

## 2. Functional requirements

### 2.1 Content ingestion (agent interface)

- REST: `POST /v1/episodes` and `POST /v1/podcasts/{id}/episodes` accepting JSON with title, text, optional show notes (`description`), voice, category, artwork (`image_url`), chapter markers (`chapters`), and `podcast_id`; `PATCH /v1/episodes/{id}` to update metadata and chapters.
- REST: `POST /v1/podcasts` to create a private show; `GET` to list / retrieve (including listener credentials and `submit_key`); `PATCH /v1/podcasts/{id}` to update show metadata; `POST /v1/podcasts/{id}/rotate` to rotate credentials.
- MCP: tools `create_podcast`, `update_podcast`, `list_podcasts`, `get_podcast`, `rotate_podcast_credentials`, `publish_agent_update`, `update_episode`, `get_episode_status`, `list_episodes`.
- Validation: minimum length, UTF-8, maximum size, slug rules, reserved path names.

### 2.2 Processing and audio synthesis

- Trigger an isolated batch process on successful ingest.
- Clean SSML/Markdown and convert scripts to `.mp3` with Kokoro-82M ONNX via `sherpa-onnx` and `ffmpeg` `-16 LUFS` 24 kHz mastering (or Piper / mock in local/dev).
- Store generated audio at `audio/<episode_id>.mp3`.

### 2.3 Podcast distribution (client interface)

- RSS 2.0 with `<enclosure>`, iTunes extensions, Podlove Simple Chapters (`<psc:chapters>`), and Podcasting 2.0 JSON Chapters (`<podcast:chapters>`).
- HTTP Basic or `?token=` on both the XML feed and audio URLs, **per show**.
- Compatible with aggregators that accept credentials in the feed URL (`https://user:password@domain/p/{id}/podcast.xml`).
- Isolation: Alice cannot read Bob's feed or audio. Default `/audio/{id}.mp3` does not serve per-user episodes.

## 3. Components

See [architecture.md](architecture.md) for the split-plane diagram.

| Component | Runtime | Hosting |
| --- | --- | --- |
| Control plane | Go 1.26+ | Cloud Run Service (or local process) |
| Data plane / worker | Go + Kokoro-82M (`sherpa-onnx`) + ffmpeg | Cloud Run Job |
| Metadata | Firestore or SQLite | GCP / local file (`episodes`, `podcasts`) |
| Assets | GCS or local directory | private bucket / `data/` |

## 4. Episode lifecycle

`PENDING` (API: `QUEUED`) → `PROCESSING` → `READY` | `FAILED`.

Only `READY` episodes of that show are rendered into its RSS.

## 5. Non-goals

- Public directory listing or Apple Podcasts Connect publishing.
- End-user signup / self-service accounts. The agent creates shows; listeners receive a subscribe URL.
- Multi-publisher tenancy (one `AGENT_API_KEY` per deployment).
- GPU inference; CPU ONNX is the supported path.
- Live / streaming audio. Episodes are finite files.
- Durable ingest queue / DLQ (ingest is a synchronous `jobs.run`).
