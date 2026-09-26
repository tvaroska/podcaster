# Architecture

This document describes the Private Podcast Platform: an automated pipeline that
turns text posted by agents into a private, password-protected podcast feed.

## Goals

- **Agent integration** — REST (`POST /v1/episodes`) and MCP (`publish_agent_update`) so Claude, custom agents, or scripts can publish without a UI.
- **Asynchronous synthesis** — TTS is CPU-heavy and duration-variable; it never blocks the ingest request.
- **Secure delivery** — RSS and enclosure URLs require HTTP Basic Auth or a feed token. The channel is marked `<itunes:block>yes</itunes:block>` so public directories do not index it.
- **Low operational overhead** — a small always-on Cloud Run service plus ephemeral Cloud Run Jobs. Local mode uses SQLite and the filesystem so you can run the same binary on a laptop.

## Split-plane design

```
                                +---------------------------+
                                |  AI Agent / Client System |
                                +---------------------------+
                                              |
                                      (REST / MCP Requests)
                                              v
+------------------------------------------------------------------------------------------+
| Control Plane: Cloud Run Service (Go)                                                    |
|  MCP Endpoint  |  REST API (POST /v1/episodes)  |  Protected RSS (GET /podcast.xml)      |
+------------------------------------------------------------------------------------------+
       |                        |                                      |
 1. Persist episode      2. Dispatch Task                    4. Audio Delivery
       v                        v                            (Reverse Proxy / Signed URL)
+-------------------+   +------------------------------------+         |
| Metadata Store    |   | Ingestion / Queue Dispatcher       |         |
| Firestore/SQLite  |   | (Cloud Run Job run / Cloud Tasks)  |         |
+-------------------+   +------------------------------------+         |
       ^                                |                              |
       | 3. CAS Status                  v                              |
       |    & Metadata  +------------------------------------+         |
       +----------------| Data Plane: Worker                 |         |
                        | (Piper ONNX + ffmpeg)              |         |
                        +------------------------------------+         |
                                        |                              |
                                   Upload MP3                          |
                                        v                              v
                        +------------------------------------------------------------------+
                        | Object Storage: GCS or local disk                                |
                        +------------------------------------------------------------------+
                                        ^                                      ^
                                        | (Target: Signed URL direct download) |
                                        |                                      |
                               +--------------------------------------------------+
                               | Podcast Client (Overcast, Pocket Casts, etc.)    |
                               | (Basic Auth / ?token= on Feed & Audio URLs)      |
                               +--------------------------------------------------+
```

### Control plane (`cmd/server`)

A single Go HTTP process:

| Surface | Path | Auth |
| --- | --- | --- |
| Health | `GET /healthz`, `GET /readyz` | none |
| Ingest | `POST /v1/episodes` | Bearer API key |
| Status | `GET /v1/episodes`, `GET /v1/episodes/{id}` | Bearer API key |
| MCP | `GET\|POST\|DELETE /mcp` | Bearer API key |
| Feed | `GET /podcast.xml` (alias `/feed.xml`) | Basic or `?token=` |
| Audio | `GET /audio/{id}.mp3` | Basic or `?token=` |
| Artwork | `GET /cover.png` | none (clients often fetch art without credentials) |

On ingest the control plane:

1. Validates UTF-8, title length, and content length.
2. Inserts an `episodes` record with `status=PENDING`.
3. Dispatches synthesis task (in-process channel locally, or Cloud Run Jobs `jobs.run` / message queue in GCP).
4. Returns `202 Accepted` with `episode_id` immediately.

RSS is rendered on each request from READY episodes. Enclosure URLs include access tokens so clients that do not replay HTTP Basic Auth on media enclosures can stream.

### Data plane (`cmd/worker`)

The worker runs as an ephemeral container or background process:

1. Atomically transitions the episode status from `PENDING` → `PROCESSING` via Compare-And-Swap (CAS) to guarantee single execution.
2. Pre-processes text: strips SSML, normalizes whitespace, chunks long scripts.
3. Runs Piper (or mock engine) to synthesize WAV chunks, concatenates chunks, and encodes 128 kbps mono MP3 via ffmpeg.
4. Uploads audio to `audio/<episode_id>.mp3`.
5. Updates metadata: status `READY`, duration, byte size, `published_at`. Failures are recorded as `FAILED` with `error_message`.

Hardware profile for Cloud Run Jobs: 2–4 vCPU, 2–4 GiB memory, no GPU (Piper ONNX runs efficiently on CPU).

### Persistence

**Metadata** (`internal/store`):

| Backend | When |
| --- | --- |
| SQLite (`modernc.org/sqlite`) | local / single instance |
| Cloud Firestore collection `episodes` | production |

Fields: `id`, `title`, `script_text`, `category`, `voice_id`, `status`, `audio_gcs_uri`, `duration_seconds`, `file_size_bytes`, `content_type`, `error_message`, `created_at`, `published_at`.

Statuses: `PENDING` → `PROCESSING` → `READY` | `FAILED`. The public API maps `PENDING` to `QUEUED`.

**Objects** (`internal/storage`):

| Backend | Layout | Delivery Mechanism |
| --- | --- | --- |
| Local directory | `{LOCAL_DATA_DIR}/audio/{id}.mp3` | Direct file stream (`http.ServeContent`) |
| GCS | `gs://{GCS_BUCKET}/audio/{id}.mp3` | Reverse-proxy (MVP) or GCS Signed URLs (Recommended) |

Optional GCS lifecycle rules can archive or delete objects older than N days.

### Audio Delivery & Streaming Architecture

1. **Current Pattern (Control Plane Reverse-Proxy):**
   - The control plane authenticates the request (`Basic Auth` or `?token=`) and streams the media file from GCS/local disk using `http.ServeContent`.
   - *Architectural Trade-offs & Critical Limitations:*
     - **Timeout Disconnections:** Cloud Run service `timeoutSeconds` (e.g., 60s) and server `WriteTimeout` (120s) impose hard cutoffs. Listeners streaming or downloading episodes over mobile connections taking longer than 60–120 seconds will be prematurely disconnected.
     - **Egress & Proxy Overhead:** Audio data traverses GCS $\to$ Cloud Run $\to$ Client, incurring double network egress costs and tying up Cloud Run container connection concurrency during slow client reads.
2. **Target Production Pattern (GCS Signed URLs / Cloud CDN):**
   - Instead of reverse-proxying raw media bytes through Cloud Run, the control plane generates a short-lived **GCS Signed URL** (or Cloud CDN signed cookie/URL) with a 15–30 minute TTL upon request, or embeds signed URLs into RSS enclosure tags.
   - The podcast client downloads directly from Google Cloud Storage / CDN. This eliminates streaming timeouts, reduces server load, and avoids double-egress costs while maintaining private bucket security.

### Ingestion & Worker Queueing Architecture

1. **Local Dispatch:**
   - In-memory buffered Go channel (buffer size: 64) with background worker goroutines.
   - *Limitation:* If the buffer is full, ingestion blocks. Server restarts drop in-memory queue state.
2. **Cloud Run Jobs (`jobs.run` per episode):**
   - The control plane invokes `runpb.RunJobRequest` directly for each ingested episode, overriding `EPISODE_ID`.
   - *Architectural Trade-offs & Scalability Limitations:*
     - **Cold-Start Latency:** Spawning a fresh container for every episode adds 10–20 seconds of cold-start latency before synthesis begins.
     - **Concurrency Quotas:** Cloud Run has a regional concurrency quota (default: 100 concurrent executions). Batch episode publishing by agents can quickly trigger quota exhaustion and HTTP 429/500 errors.
     - **Lack of Backpressure / DLQ:** Direct invocation lacks native queue buffering, rate-limiting, and dead-letter queues.
3. **Target Production Pattern (Cloud Tasks / Pub/Sub):**
   - Ingest pushes episode IDs to Google Cloud Tasks or Pub/Sub.
   - A dedicated worker pool (or auto-scaling Cloud Run service) processes tasks with configurable concurrency, exponential backoff, rate limits, and dead-letter queues.

### Worker Lifecycle, Idempotency & Retries

- **Compare-And-Swap (CAS):** The worker uses an atomic CAS update (`PENDING` $\to$ `PROCESSING`) to ensure duplicate job executions do not synthesize the same episode concurrently.
- **Retry Handling & Status Conflicts:** If a worker encounters an error, it records `FAILED` with `error_message`. Note that automatic container retries (e.g., `maxRetries: 1` in Cloud Run Jobs) will fail CAS if the status has transitioned to `FAILED`. Production resilience requires allowing retry transitions (`FAILED` $\to$ `PROCESSING`) or implementing a supervisor reconciliation loop.
- **Stranded Episode Recovery:** Ungraceful worker termination can leave episodes stuck in `PROCESSING`. Production deployments require a periodic startup sweep or reconciler to reset expired `PROCESSING` records back to `PENDING`.

## Processing sequence

```
Agent                 Server                 Store           Worker               Object Store / CDN
  |                     |                      |                |                     |
  |-- POST /v1/episodes->|                      |                |                     |
  |                     |-- Create PENDING ---->|                |                     |
  |                     |-- Enqueue / RunJob ------------------>|                     |
  |<- 202 QUEUED -------|                      |                |                     |
  |                     |                      |<- CAS PROCESS--|                     |
  |                     |                      |                |-- Piper + ffmpeg    |
  |                     |                      |                |-- Put MP3 --------->|
  |                     |                      |<- READY -------|                     |
  |-- GET /podcast.xml->|                      |                |                     |
  |                     |-- List READY ------->|                |                     |
  |<- RSS + enclosure --|                      |                |                     |
  |                     |                      |                |                     |
  | [Pattern A: Reverse-Proxy (Current)]       |                |                     |
  |-- GET /audio/id.mp3-|                      |                |                     |
  |                     |-- Read stream --------------------------------------------->|
  |<- audio/mpeg stream-|                      |                |                     |
  |                     |                      |                |                     |
  | [Pattern B: Signed URL (Target)]           |                |                     |
  |-- GET /audio/id.mp3-|                      |                |                     |
  |<- 302 Signed URL ---|                      |                |                     |
  |-- GET <signed-gcs-url> ---------------------------------------------------------->|
  |<- audio/mpeg direct stream ------------------------------------------------------->|
```

## Security model

- **Agent plane**: `Authorization: Bearer <AGENT_API_KEY>`. Compared in constant time. Used for REST and MCP.
- **Listener plane**: HTTP Basic (`FEED_USERNAME` / `FEED_PASSWORD`) or `?token=<FEED_TOKEN>`. Podcast apps subscribe with `https://user:password@host/podcast.xml`.
- **Feed & Enclosure Access**:
  - *Current behavior:* `?token=<FEED_TOKEN>` is appended to media enclosure URLs in RSS feeds for client compatibility.
  - *Security Consideration:* Sharing an episode link exposes the permanent master feed token. Production deployments should transition to short-lived, episode-specific media tokens or signed storage URLs.
- **Assets**: GCS objects are kept private. Streaming requires authentication via the control plane or expiring signed URLs.
- **Feed**: `itunes:block=yes` keeps the show out of Apple/Google indexes even if the URL leaks.

## Local vs GCP mapping

| Concern | Local | GCP |
| --- | --- | --- |
| HTTP | `cmd/server` on `:8080` | Cloud Run Service |
| TTS job | in-process worker pool | Cloud Run Job |
| Metadata | SQLite file | Firestore |
| Audio | `data/audio/` | private GCS bucket |
| TTS engine | `TTS_ENGINE=mock` or Piper | Piper ONNX in the job image |

Switching is configuration only (`STORE_BACKEND`, `STORAGE_BACKEND`, `JOB_BACKEND`, `TTS_ENGINE`). The application code is shared.
