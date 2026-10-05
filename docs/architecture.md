# Architecture

Podcaster turns text posted by an agent into private, password-protected podcast feeds. One publisher (`AGENT_API_KEY`); many shows (one per listener).

## Goals

- **Agent integration** — REST and MCP so Claude, custom agents, or scripts can publish without a UI.
- **Per-listener shows** — `POST /v1/podcasts` creates a slug, unique Basic credentials, and a feed at `/p/{id}/podcast.xml`. The default `/podcast.xml` is a separate show using `FEED_*`.
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
  | REST, MCP, RSS, audio     |                   | Piper + ffmpeg   |
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
| Shows | `POST/GET /v1/podcasts`, `GET /v1/podcasts/{id}` | Bearer |
| Ingest | `POST /v1/episodes`, `POST /v1/podcasts/{id}/episodes` | Bearer |
| Status | `GET /v1/episodes`, `GET /v1/episodes/{id}` | Bearer |
| MCP | `GET\|POST\|DELETE /mcp` | Bearer |
| Default feed | `GET /podcast.xml` (alias `/feed.xml`) | `FEED_*` Basic or `?token=` |
| Default audio | `GET /audio/{id}.mp3` | same |
| Per-show feed | `GET /p/{id}/podcast.xml` | that show's Basic or token |
| Per-show audio | `GET /p/{id}/audio/{ep}.mp3` | same |
| Artwork | `GET /cover.png`, `GET /p/{id}/cover.png` | none (clients often fetch art without credentials) |

On ingest the control plane:

1. Validates UTF-8, title length, content length, and optional `podcast_id` (must exist).
2. Inserts an `episodes` record with `status=PENDING` (API maps this to `QUEUED`).
3. Dispatches synthesis (in-process channel locally, or Cloud Run Jobs `jobs.run` with `EPISODE_ID`).
4. Returns `202 Accepted` with `episode_id` immediately.

RSS is rendered on each request from `READY` episodes of **that show**. Default feed lists only episodes with empty `podcast_id`. Enclosure URLs include that show's token so clients that do not replay Basic auth on media still play.

### Data plane (`cmd/worker`)

1. CAS `PENDING` → `PROCESSING` so duplicate executions do not synthesize twice.
2. Strip SSML, normalize whitespace, chunk long scripts.
3. Piper (or mock) → WAV → 128 kbps mono MP3 via ffmpeg.
4. Upload `audio/<episode_id>.mp3` (object key is episode id, not show id).
5. `READY` with duration and byte size, or `FAILED` with `error_message`.

Cloud Run Jobs: 2 vCPU, 2 GiB, no GPU. First execution after deploy is often 2–3 minutes (image pull).

## Persistence

**Metadata** (`internal/store`):

| Backend | When |
| --- | --- |
| SQLite (`modernc.org/sqlite`) | local / single instance |
| Cloud Firestore | production: collections `episodes` and `podcasts` |

Episode fields: `id`, `podcast_id`, `title`, `script_text`, `category`, `voice_id`, `status`, `audio_gcs_uri`, `duration_seconds`, `file_size_bytes`, `content_type`, `error_message`, `created_at`, `published_at`.

Show fields: `id` (slug), `title`, `description`, `author`, `username`, `password`, `token`, `created_at`. Listener secrets are stored **plaintext** in this document.

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
  |                     |                      |                |-- Piper+ffmpeg   |
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

- **Agent plane:** `Authorization: Bearer <AGENT_API_KEY>`, constant-time compare. REST and MCP. `GET /v1/podcasts/{id}` returns that show's password — treat the agent key as the owner credential.
- **Listener plane:** per-show HTTP Basic or `?token=`. Default show uses `FEED_*` (Secret Manager in GCP). User shows use credentials stored on the `podcasts` document.
- Sharing an episode link leaks that show's permanent token. Tokens are not shared across shows. Default-show creds cannot read `/p/{id}/...`.
- GCS objects are private. Playback uses a short-lived signed URL after the control plane has authenticated the listener.
- `itunes:block=yes` keeps the show out of Apple/Google indexes even if the URL leaks.

## Local vs GCP

| Concern | Local | GCP |
| --- | --- | --- |
| HTTP | `cmd/server` on `:8080` | Cloud Run Service |
| TTS job | in-process worker pool | Cloud Run Job |
| Metadata | SQLite file | Firestore |
| Audio | `data/audio/` | private GCS bucket |
| TTS engine | `TTS_ENGINE=mock` or Piper | Piper ONNX in the job image |
| Default-show secrets | env / documented defaults | Secret Manager |
| Per-user secrets | `podcasts` table | Firestore `podcasts` |

Switching is configuration (`STORE_BACKEND`, `STORAGE_BACKEND`, `JOB_BACKEND`, `TTS_ENGINE`). The application code is shared.
