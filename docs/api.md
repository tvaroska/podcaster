# API reference

Base URL is `PUBLIC_BASE_URL` (for example `http://localhost:8080` or `https://podcast.example.com`).

## Authentication

### Agent (ingest + MCP)

```
Authorization: Bearer <AGENT_API_KEY>
```

Missing or incorrect keys return `401` with `{"error":"invalid or missing bearer token"}`.

### Listener (feed + audio)

Each **show** has its own credentials. Failed listener auth returns `401` plus `WWW-Authenticate: Basic realm="Private Podcast"`.

**Default show** (`/podcast.xml`, `/audio/...`) uses `FEED_USERNAME` / `FEED_PASSWORD` / `FEED_TOKEN`.

**Per-user show** (`/p/{id}/podcast.xml`, `/p/{id}/audio/...`) uses the username, password, and token returned when the show was created. Alice cannot read Bob's feed or audio.

Subscribe:

```
https://USERNAME:PASSWORD@host/p/alice/podcast.xml
```

Enclosure URLs already include that show's `?token=` so clients that do not replay Basic Auth on media still play. Tokens are **not** shared across shows.

## REST

### `POST /v1/podcasts`

Create a private show for one listener. The agent is the publisher; the listener only gets a feed URL.

```http
POST /v1/podcasts HTTP/1.1
Authorization: Bearer <AGENT_API_KEY>
Content-Type: application/json

{"id":"alice","title":"Alice Briefing","description":"Private updates for Alice"}
```

`id` is the URL slug: 2–32 chars, lowercase letter first, then letters/digits/hyphens. Reserved names (`v1`, `audio`, `podcast`, …) are rejected. `409` if it already exists.

**Response `201 Created`**

```json
{
  "id": "alice",
  "title": "Alice Briefing",
  "description": "Private updates for Alice",
  "author": "Podcaster",
  "username": "alice",
  "password": "<random>",
  "token": "<random>",
  "feed_url": "https://host/p/alice/podcast.xml",
  "subscribe_url": "https://alice:<password>@host/p/alice/podcast.xml",
  "created_at": "2026-09-26T13:30:00Z"
}
```

Also: `GET /v1/podcasts`, `GET /v1/podcasts/{id}` (same payload, including secrets — treat the agent key as the owner credential). Listener passwords are stored on the show document in SQLite/Firestore.

`404` unknown id on `GET /v1/podcasts/{id}`. `422` invalid slug.

### `POST /v1/podcasts/{id}/episodes`

Same body as `POST /v1/episodes`, scoped to that show. Equivalent to sending `"podcast_id":"{id}"` on the default ingest path.

### `POST /v1/episodes`

Queue a new episode. Synthesis runs asynchronously.

**Request**

```http
POST /v1/episodes HTTP/1.1
Authorization: Bearer <AGENT_API_KEY>
Content-Type: application/json

{
  "title": "Morning Briefing - Sept 26, 2026",
  "content": "Good morning. Here are your top updates for today...",
  "voice_id": "en_US-lessac-medium",
  "category": "Daily Briefing",
  "podcast_id": "alice"
}
```

| Field | Required | Notes |
| --- | --- | --- |
| `title` | yes | 1–200 characters (override with `MAX_TITLE_LENGTH`) |
| `content` | yes | 10–100 000 characters (`MIN_CONTENT_LENGTH`, `MAX_CONTENT_LENGTH`), valid UTF-8. SSML tags are stripped before TTS. |
| `voice_id` | no | Defaults to `DEFAULT_VOICE`. Rejected if `VOICE_ALLOWLIST` is set and the id is not listed. |
| `category` | no | Shown in the RSS `<category>` and description prefix. |
| `podcast_id` | no | Show slug. Empty publishes to the default `/podcast.xml` feed. |

Unknown JSON fields are rejected (`400`).

**Response `202 Accepted`**

```json
{
  "episode_id": "ep_8f9a2b1c0d1e2f3a",
  "podcast_id": "alice",
  "status": "QUEUED",
  "created_at": "2026-09-26T13:30:00Z"
}
```

(`podcast_id` is omitted when publishing to the default feed.)

| Status | Meaning |
| --- | --- |
| `400` | Malformed JSON |
| `401` | Bad bearer token |
| `422` | Validation error (`{"error":"content: content must be at least 10 characters"}` or `{"error":"podcast_id: podcast not found"}`) |
| `500` | Persist or enqueue failure |

### `GET /v1/episodes`

List recent episodes (newest first). Query: `status`, `podcast_id`, `limit` (default 50, max 100), `offset`.

```json
{
  "episodes": [
    {
      "episode_id": "ep_8f9a2b1c0d1e2f3a",
      "podcast_id": "alice",
      "title": "Morning Briefing - Sept 26, 2026",
      "status": "READY",
      "category": "Daily Briefing",
      "duration_seconds": 42.1,
      "created_at": "2026-09-26T13:30:00Z"
    }
  ]
}
```

### `GET /v1/episodes/{id}`

```json
{
  "episode_id": "ep_8f9a2b1c0d1e2f3a",
  "podcast_id": "alice",
  "title": "Morning Briefing - Sept 26, 2026",
  "status": "READY",
  "category": "Daily Briefing",
  "voice_id": "en_US-lessac-medium",
  "duration_seconds": 42.1,
  "file_size_bytes": 675840,
  "error_message": "",
  "created_at": "2026-09-26T13:30:00Z",
  "published_at": "2026-09-26T13:30:08Z"
}
```

`status` is one of `QUEUED`, `PROCESSING`, `READY`, `FAILED`.

### `GET /p/{id}/podcast.xml` (alias `GET /p/{id}/feed.xml`)

That show's RSS. Only its `READY` episodes. Auth: that show's Basic or `?token=`.

Enclosure URLs are `/p/{id}/audio/{episode_id}.mp3?token=...`. Cover: `/p/{id}/cover.png`.

### `GET /podcast.xml` (alias `GET /feed.xml`)

Default show RSS. Only episodes **without** a `podcast_id`. Auth: `FEED_USERNAME` / `FEED_PASSWORD`.

Notable channel tags:

- `itunes:author`, `itunes:image`, `itunes:category`, `itunes:explicit`
- `itunes:block` = `yes` (private)
- `atom:link rel="self"`
- items with `enclosure url length type`, `itunes:duration`, `guid`

### `GET /p/{id}/audio/{episode_id}.mp3`

That show's enclosure. Auth: that show's Basic or `?token=`. Returns **404** if the episode belongs to a different show (including the default show). `HEAD` is supported.

On GCS, a successful auth **307**s to a short-lived signed object URL. If signing is unavailable the service streams the bytes itself.

### `GET /audio/{episode_id}.mp3`

Default-show enclosure only (empty `podcast_id`). Same 307 behaviour. Per-user episodes are **404** here even with default credentials.

### `GET /cover.png` / `GET /p/{id}/cover.png`

1400×1400 PNG artwork. No auth (clients often fetch art without credentials). Override the generated image with `PODCAST_IMAGE_FILE`.

### `GET /healthz` / `GET /readyz`

Liveness is always `{"status":"ok"}`. Readiness pings the metadata store. From the public internet on Cloud Run, `/healthz` may be intercepted by Google's frontend (HTML 404); use `/readyz`.

## MCP

Streamable HTTP endpoint: `POST /mcp` (also `GET` / `DELETE` for session lifecycle). Same bearer token as REST.

Server implementation: `podcaster` v0.1.0.

### Tool `create_podcast`

| Input | Required | Description |
| --- | --- | --- |
| `id` | yes | URL slug (`alice`) |
| `title` | yes | Show title |
| `description` | no | RSS description |
| `author` | no | `itunes:author` |

Returns `id`, `title`, `username`, `password`, `token`, `feed_url`, `subscribe_url`, and `message`.

### Tool `publish_agent_update`

| Input | Required | Description |
| --- | --- | --- |
| `title` | yes | Episode title |
| `content` | yes | Plain-text script |
| `category` | no | e.g. `Daily Briefing` |
| `voice_id` | no | Piper voice id |
| `podcast_id` | no | Show slug. Empty publishes to the default feed. |

**Output**

```json
{
  "status": "QUEUED",
  "episode_id": "ep_8f9a2b1c0d1e2f3a",
  "message": "Batch job initialized. Episode will appear in the feed once synthesis completes."
}
```

### Tool `get_episode_status`

Input: `episode_id`. Returns `episode_id`, `title`, `status`, `duration_seconds` (if set), `error_message` (if set), `created_at`, and `published_at` (if set).

### Claude Desktop / Claude Code example

```json
{
  "mcpServers": {
    "podcaster": {
      "url": "https://podcast.example.com/mcp",
      "headers": {
        "Authorization": "Bearer ${AGENT_API_KEY}"
      }
    }
  }
}
```

For stdio-style local development, point an MCP HTTP client at `http://localhost:8080/mcp`.

## cURL examples

```bash
# create a private show
curl -sS -X POST "$BASE/v1/podcasts" \
  -H "Authorization: Bearer $AGENT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"id":"alice","title":"Alice Briefing","description":"Private updates for Alice"}'

# publish to that show
curl -sS -X POST "$BASE/v1/podcasts/alice/episodes" \
  -H "Authorization: Bearer $AGENT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"title":"Morning Briefing","content":"Good morning. Here are your top updates for today."}'

# poll
curl -sS "$BASE/v1/episodes/ep_..." -H "Authorization: Bearer $AGENT_API_KEY"

# that show's feed (use the password from create)
curl -sS -u "alice:$ALICE_PASSWORD" "$BASE/p/alice/podcast.xml"

# default show (env FEED_*)
curl -sS -u "$FEED_USERNAME:$FEED_PASSWORD" "$BASE/podcast.xml"
```
