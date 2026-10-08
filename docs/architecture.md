# Architecture

Podcaster turns text posted by an agent into private, password-protected podcast feeds, with separated **Super Key** (`ADMIN_API_KEY`), **Per-User Submit Key** (`submit_key`), and **Per-User Listening Key** (`password` / `token`) credentials.

## Goals

- **Agent integration** — REST and MCP so Claude, custom agents, or scripts can publish without a UI.
- **Per-listener shows & scoped keys** — `POST /v1/podcasts` creates a slug, a per-show publisher `submit_key`, unique listener Basic/token credentials, and a feed at `/p/{id}/podcast.xml` (with customizable `title`, `description`, `author`, and `image_url` via `PATCH /v1/podcasts/{id}` / `update_podcast`). The default `/podcast.xml` is a separate show using `FEED_*`.
- **Rich episode metadata & chapters** — Episodes support show notes (`description`), per-episode artwork (`image_url`), and chapter markers (`chapters`), rendered in RSS via `<itunes:summary>`, `<content:encoded>`, `<itunes:image>`, Podlove Simple Chapters (`<psc:chapters>`), and Podcasting 2.0 JSON Chapters (`<podcast:chapters>`), and updatable post-publish via `PATCH /v1/episodes/{id}` / `update_episode`.
- **Asynchronous synthesis** — TTS never blocks the ingest request.
- **Secure delivery** — RSS and enclosure URLs require HTTP Basic or a feed token. Channels include `<itunes:block>yes</itunes:block>`.
- **Low operational overhead** — a small Cloud Run service plus ephemeral Cloud Run Jobs. Local mode uses SQLite and the filesystem.

## Split-plane design

```
  AI agent (REST / MCP)
           |
           v
  +---------------------------+     jobs.run      +------------------+
  | Control plane             | ----------------> | Worker           |
  | cmd/server                |   EPISODE_ID      | cmd/worker       |
  | REST, MCP, RSS, audio     |                   | Kokoro + ffmpeg  |
  +-------------+-------------+                   +--------+---------+
                |                                          |
                v                                          | Put MP3
  SQLite / Firestore                              local disk / private GCS
  episodes + podcasts                                      |
                ^                                          |
                |                                          v
                +------ GET /p/{id}/podcast.xml -----------+
                        Basic or ?token=
                                   |
                          podcast app
```

### Control plane (`cmd/server`)

| Surface | Path | Auth |
| --- | --- | --- |
| Health | `GET /healthz`, `GET /readyz` | none. Public `/healthz` may be intercepted by the Cloud Run GFE; `/readyz` is the app. |
| Shows | `POST/GET /v1/podcasts`, `GET/PATCH/PUT /v1/podcasts/{id}`, `POST /v1/podcasts/{id}/rotate` | Bearer (Super Key for create/list; Super Key or show `submit_key` for get/update/rotate) |
| Ingest | `POST /v1/episodes`, `POST /v1/podcasts/{id}/episodes` | Bearer (Super Key, default `AGENT_API_KEY`, or show `submit_key`) |
| Episodes | `GET /v1/episodes`, `GET/PATCH/PUT /v1/episodes/{id}` | Bearer (scoped to caller's key tier) |
| MCP | `GET\|POST\|DELETE /mcp` | Bearer (3-tier key hierarchy) |
| Default feed | `GET /podcast.xml` (alias `/feed.xml`) | `FEED_*` Basic or `?token=` |
| Default audio / chapters | `GET /audio/{id}.mp3`, `GET /episodes/{id}/chapters.json` | same |
| Per-show feed | `GET /p/{id}/podcast.xml` | that show's Basic or token |
| Per-show audio / chapters | `GET /p/{id}/audio/{ep}.mp3`, `GET /p/{id}/episodes/{ep}/chapters.json` | same |
| Artwork | `GET /cover.png`, `GET /p/{id}/cover.png` | none (clients often fetch art without credentials) |

On ingest the control plane:

1. Validates UTF-8, title length, content length, optional `description`, `image_url`, `chapters`, and optional `podcast_id` (must exist).
2. Inserts an `episodes` record with `status=PENDING` (API maps this to `QUEUED`).
3. Dispatches synthesis (in-process channel locally, or Cloud Run Jobs `jobs.run` with `EPISODE_ID`).
4. Returns `202 Accepted` with `episode_id` immediately.

RSS is rendered on each request from `READY` episodes of **that show**. Default feed lists only episodes with empty `podcast_id`. Channel `<itunes:image>` uses the show's custom `image_url` when set (falling back to `/cover.png` or `/p/{id}/cover.png`). Episode `<item>` elements include `<description>`, `<itunes:summary>`, `<content:encoded>`, optional per-episode `<itunes:image>`, and when `chapters` are present both inline Podlove Simple Chapters (`<psc:chapters>`) and a Podcasting 2.0 `<podcast:chapters>` link. Enclosure and chapter URLs include that show's token so clients that do not replay Basic auth on media still play.

### Data plane (`cmd/worker`)

1. CAS `PENDING` → `PROCESSING` so duplicate executions do not synthesize twice.
2. Strip SSML and Markdown formatting, normalize whitespace, and split into paragraph- and sentence-aware chunks (<= 500 runes).
3. Kokoro-82M via `sherpa-onnx` (or Piper / mock) → WAV → 128 kbps 24 kHz mono MP3 mastered to `-16 LUFS` via ffmpeg.
4. Upload `audio/<episode_id>.mp3` (object key is episode id, not show id).
5. `READY` with duration and byte size, or `FAILED` with `error_message`.

Cloud Run Jobs: 2 vCPU, 2 GiB, no GPU. First execution after deploy is often 2–3 minutes (image pull).

## Persistence

**Metadata** (`internal/store`):

| Backend | When |
| --- | --- |
| SQLite (`modernc.org/sqlite`) | local / single instance |
| Cloud Firestore | production: collections `episodes` and `podcasts` |

Episode fields: `id`, `podcast_id`, `title`, `description`, `script_text`, `category`, `voice_id`, `image_url`, `chapters` (`chapters_json` in SQLite / `chapters` in Firestore), `status`, `audio_gcs_uri`, `duration_seconds`, `file_size_bytes`, `content_type`, `error_message`, `created_at`, `published_at`.

Show fields: `id` (slug), `title`, `description`, `author`, `image_url`, `username`, `password`, `token`, `submit_key`, `created_at`. Listener and per-show submit secrets are stored **plaintext** in this document.

Statuses: `PENDING` → `PROCESSING` → `READY` | `FAILED`. Public API maps `PENDING` to `QUEUED`.

**Objects** (`internal/storage`):

| Backend | Layout | Delivery |
| --- | --- | --- |
| Local directory | `{LOCAL_DATA_DIR}/audio/{id}.mp3` | `http.ServeContent` |
| GCS | `gs://{GCS_BUCKET}/audio/{id}.mp3` | After auth, **307** to a short-lived signed URL. If `signBlob` fails, the service proxies bytes. |

GCS lifecycle (`deploy/gcs-lifecycle.json`) moves objects to Nearline after 90 days.

## Audio delivery

Authenticated `GET /audio/...` (or `/p/{id}/audio/...`):

1. Check Basic / `?token=` for **that show**.
2. Reject if the episode's `podcast_id` does not match the URL (default `/audio` only serves empty `podcast_id`).
3. If storage implements `SignedURL`, redirect **307** to GCS (30-minute TTL; 15-minute default if unspecified). The podcast client then downloads from GCS.
4. Otherwise stream through the control plane (`http.ServeContent`).

Enclosure URLs in RSS still point at the control plane (`?token=`), not at GCS. The signed URL is minted at play time.

## Ingestion / queueing

- **Local:** in-memory Go channel (buffer 64) + worker goroutines. Full buffer blocks ingest. Restarts drop the queue (rows stay `PENDING`; startup `Reconcile` re-enqueues them).
- **GCP:** one `jobs.run` RPC per ingest. Not a durable queue. Default Jobs concurrency quota is 100. No DLQ.
- **Not built:** Cloud Tasks / Pub/Sub with backoff and a dead-letter queue.

## Worker lifecycle

- CAS `PENDING` → `PROCESSING` is the primary lock. `READY` episodes are skipped as a no-op.
- Job `maxRetries: 1`. On retry, the worker can re-claim `FAILED` episodes via CAS (`FAILED` → `PROCESSING`) or reclaim an interrupted `PROCESSING` episode when `CLOUD_RUN_TASK_ATTEMPT` is non-zero; otherwise concurrent workers skip `PROCESSING` episodes.
- `Reconcile` on **service start** resets any stranded `PROCESSING` episodes back to `PENDING` via CAS and re-enqueues all `PENDING` episodes. With Cloud Run `--min-instances=0` a stranded episode waits for the next cold start.

## Processing sequence

```
Agent                 Server                 Store           Worker               GCS
  |                     |                      |                |                  |
  |-- POST /v1/podcasts/{id}/episodes -------->|                |                  |
  |                     |-- Create PENDING --->|                |                  |
  |                     |-- RunJob(EPISODE_ID) ---------------->|                  |
  |<- 202 QUEUED -------|                      |                |                  |
  |                     |                      |<- CAS PROCESS--|                  |
  |                     |                      |                |-- Kokoro+ffmpeg  |
  |                     |                      |                |-- Put MP3 ------>|
  |                     |                      |<- READY -------|                  |
  |-- GET /p/{id}/podcast.xml ---------------->|                |                  |
  |<- RSS + enclosure --|                      |                |                  |
  |-- GET /p/{id}/audio/ep.mp3?token= ---------|                |                  |
  |<- 307 signed GCS --------------------------------------------------------------|
  |-- GET <signed-gcs-url> ------------------------------------------------------->|
  |<- audio/mpeg -------------------------------------------------------------------|
```

## Security model

- **Agent / Publisher plane (3-tier Bearer auth, SHA-256 constant-time compare):**
  - **Super Key (`ADMIN_API_KEY`, or `AGENT_API_KEY` when `ADMIN_API_KEY` is unset):** Can create and list shows (`POST /v1/podcasts`, `GET /v1/podcasts`), update any show (`PATCH /v1/podcasts/{id}` / MCP `update_podcast`), rotate any show's listener or submit keys (`POST /v1/podcasts/{id}/rotate` / MCP `rotate_podcast_credentials`), and publish/update/read episodes across all shows.
  - **Per-User Submit Key (`submit_key` returned on `POST /v1/podcasts`):** Scoped exclusively to show `{id}`. Can publish, update (`PATCH /v1/episodes/{ep}` / MCP `update_episode`), and read episodes for `{id}`, view `{id}` (`GET /v1/podcasts/{id}`), update `{id}`'s metadata (`PATCH /v1/podcasts/{id}` / MCP `update_podcast`), and rotate `{id}`'s listening credentials (`POST /v1/podcasts/{id}/rotate`). Cannot create/list other shows (`403`) and cannot read the RSS feed or MP3s directly.
  - **Default-Feed Submit Key (`AGENT_API_KEY` when `ADMIN_API_KEY` is also set):** Scoped exclusively to publishing, updating (`PATCH /v1/episodes/{id}` / MCP `update_episode`), and listing episodes on the default feed (`podcast_id == ""`).
- **Listener plane (Per-User Listening Key):** Per-show HTTP Basic (`username` / `password`) or `?token=`. Default show uses `FEED_*` (Secret Manager in GCP). User shows use `password` and `token` stored on the `podcasts` document, separated from the publisher `submit_key`.
- **Credential rotation:** Sharing an episode link leaks that show's `?token=`. Tokens are not shared across shows, and default-show creds cannot read `/p/{id}/...`. A leaked listener `password` or `token` (or `submit_key`) can be rotated at any time via `POST /v1/podcasts/{id}/rotate` or the `rotate_podcast_credentials` MCP tool.
- GCS objects are private. Playback uses a short-lived signed URL after the control plane has authenticated the listener.
- `itunes:block=yes` keeps the show out of Apple/Google indexes even if the URL leaks.

## Local vs GCP

| Concern | Local | GCP |
| --- | --- | --- |
| HTTP | `cmd/server` on `:8080` | Cloud Run Service |
| TTS job | in-process worker pool | Cloud Run Job |
| Metadata | SQLite file | Firestore |
| Audio | `data/audio/` | private GCS bucket |
| TTS engine | `TTS_ENGINE=mock`, `kokoro`, or `piper` | Kokoro-82M ONNX (`sherpa-onnx`) in the job image |
| Default-show secrets | env / documented defaults | Secret Manager |
| Per-user secrets | `podcasts` table | Firestore `podcasts` |

Switching is configuration (`STORE_BACKEND`, `STORAGE_BACKEND`, `JOB_BACKEND`, `TTS_ENGINE`). The application code is shared.
