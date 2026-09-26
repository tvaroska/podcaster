# API reference

Base URL is `PUBLIC_BASE_URL` (for example `http://localhost:8080` or `https://podcast.example.com`).

## Authentication

### Agent (ingest + MCP)

```
Authorization: Bearer <AGENT_API_KEY>
```

Missing or incorrect keys return `401` with `{"error":"invalid or missing bearer token"}`.

### Listener (feed + audio)

Either:

- HTTP Basic Auth with `FEED_USERNAME` / `FEED_PASSWORD`, or
- query parameter `?token=<FEED_TOKEN>`.

Failed listener auth returns `401` plus `WWW-Authenticate: Basic realm="Private Podcast"`.

Subscribe in Overcast / Pocket Casts / Apple Podcasts with:

```
https://FEED_USERNAME:FEED_PASSWORD@host/podcast.xml
```

Enclosure URLs in the feed already include `?token=` so clients that do not replay Basic Auth on media still play.

## REST

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
  "category": "Daily Briefing"
}
```

| Field | Required | Notes |
| --- | --- | --- |
| `title` | yes | 1–200 characters (override with `MAX_TITLE_LENGTH`) |
| `content` | yes | 10–100 000 characters (`MIN_CONTENT_LENGTH`, `MAX_CONTENT_LENGTH`), valid UTF-8. SSML tags are stripped before TTS. |
| `voice_id` | no | Defaults to `DEFAULT_VOICE`. Rejected if `VOICE_ALLOWLIST` is set and the id is not listed. |
| `category` | no | Shown in the RSS `<category>` and description prefix. |

Unknown JSON fields are rejected (`400`).

**Response `202 Accepted`**

```json
{
  "episode_id": "ep_8f9a2b1c0d1e2f3a",
  "status": "QUEUED",
  "created_at": "2026-09-26T13:30:00Z"
}
```

| Status | Meaning |
| --- | --- |
| `400` | Malformed JSON |
| `401` | Bad bearer token |
| `422` | Validation error (`{"error":"content: content must be at least 10 characters"}`) |
| `500` | Persist or enqueue failure |

### `GET /v1/episodes`

List recent episodes (newest first). Query: `status`, `limit` (default 50, max 100), `offset`.

```json
{
  "episodes": [
    {
      "episode_id": "ep_8f9a2b1c0d1e2f3a",
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

### `GET /podcast.xml` (alias `GET /feed.xml`)

RSS 2.0 + iTunes tags. Only `READY` episodes appear.

Notable channel tags:

- `itunes:author`, `itunes:image`, `itunes:category`, `itunes:explicit`
- `itunes:block` = `yes` (private)
- `atom:link rel="self"`
- items with `enclosure url length type`, `itunes:duration`, `guid`

### `GET /audio/{episode_id}.mp3`

Authenticated byte-range stream of the enclosure. `HEAD` is supported.

### `GET /cover.png`

1400×1400 PNG artwork. Override the generated image with `PODCAST_IMAGE_FILE`.

### `GET /healthz` / `GET /readyz`

Liveness is always `{"status":"ok"}`. Readiness pings the metadata store.

## MCP

Streamable HTTP endpoint: `POST /mcp` (also `GET` / `DELETE` for session lifecycle). Same bearer token as REST.

Server implementation: `podcaster` v0.1.0.

### Tool `publish_agent_update`

| Input | Required | Description |
| --- | --- | --- |
| `title` | yes | Episode title |
| `content` | yes | Plain-text script |
| `category` | no | e.g. `Daily Briefing` |
| `voice_id` | no | Piper voice id |

**Output**

```json
{
  "status": "QUEUED",
  "episode_id": "ep_8f9a2b1c0d1e2f3a",
  "message": "Batch job initialized. Episode will appear in the feed once synthesis completes."
}
```

### Tool `get_episode_status`

Input: `episode_id`. Output mirrors the REST GET payload.

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
# enqueue
curl -sS -X POST "$BASE/v1/episodes" \
  -H "Authorization: Bearer $AGENT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"title":"Morning Briefing","content":"Good morning. Here are your top updates for today."}'

# poll
curl -sS "$BASE/v1/episodes/ep_..." -H "Authorization: Bearer $AGENT_API_KEY"

# feed
curl -sS -u "$FEED_USERNAME:$FEED_PASSWORD" "$BASE/podcast.xml"

# audio
curl -sS -u "$FEED_USERNAME:$FEED_PASSWORD" -o episode.mp3 "$BASE/audio/ep_....mp3"
```
