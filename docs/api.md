# API reference

Base URL is `PUBLIC_BASE_URL` (for example `http://localhost:8080` or `https://podcast.example.com`).

## Authentication

### Agent / Publisher (ingest + MCP)

```
Authorization: Bearer <API_KEY>
```

Missing or incorrect keys return `401` with `{"error":"invalid or missing bearer token"}`. Valid keys attempting an out-of-scope action return `403 Forbidden`.

Podcaster supports a **3-tier publisher key hierarchy**:

1. **Super Key (`ADMIN_API_KEY`, or `AGENT_API_KEY` if `ADMIN_API_KEY` is unset)**: Can create podcasts (`POST /v1/podcasts`), list all podcasts (`GET /v1/podcasts`), update any podcast (`PATCH /v1/podcasts/{id}`), rotate any podcast's listener or submit keys (`POST /v1/podcasts/{id}/rotate`), and publish/update/read episodes across all shows.
2. **Per-User Submit Key (`submit_key` returned on `POST /v1/podcasts`)**: Scoped to a single show `{id}`. Can publish episodes (`POST /v1/podcasts/{id}/episodes` or `POST /v1/episodes`), update episodes (`PATCH /v1/episodes/{ep}`) for `{id}`, list/get episodes for `{id}`, view `{id}` (`GET /v1/podcasts/{id}`), update `{id}`'s metadata (`PATCH /v1/podcasts/{id}`), and rotate `{id}`'s listening credentials (`POST /v1/podcasts/{id}/rotate`). Cannot create/list other shows (`403 Forbidden`) and cannot read the RSS feed or MP3s directly.
3. **Default-Feed Submit Key (`AGENT_API_KEY` when `ADMIN_API_KEY` is also set)**: Scoped to publishing, updating (`PATCH /v1/episodes/{id}`), and listing episodes on the default feed (`podcast_id == ""`).

### Listener (feed + audio)

Each **show** has its own listening credentials (`username`, `password`, `token`), separate from its publisher `submit_key`. Failed listener auth returns `401` plus `WWW-Authenticate: Basic realm="Private Podcast"`.

**Default show** (`/podcast.xml`, `/audio/...`) uses `FEED_USERNAME` / `FEED_PASSWORD` / `FEED_TOKEN`.

**Per-user show** (`/p/{id}/podcast.xml`, `/p/{id}/audio/...`) uses the username, password, and token returned when the show was created (or last rotated via `POST /v1/podcasts/{id}/rotate`). Alice cannot read Bob's feed or audio, and a show's `submit_key` cannot read the RSS feed or audio.

Subscribe:

```
https://USERNAME:PASSWORD@host/p/alice/podcast.xml
```

Enclosure URLs already include that show's `?token=` so clients that do not replay Basic Auth on media still play. Tokens are **not** shared across shows.

## REST

### `POST /v1/podcasts`

Create a private show for one listener. Requires the **Super Key** (`ADMIN_API_KEY`, or `AGENT_API_KEY` when `ADMIN_API_KEY` is unset). Returns both a per-user `submit_key` (for publishing to this show) and listener credentials (`username`, `password`, `token`, `subscribe_url`).

```http
POST /v1/podcasts HTTP/1.1
Authorization: Bearer <ADMIN_OR_AGENT_API_KEY>
Content-Type: application/json

{"id":"alice","title":"Alice Briefing","description":"Private updates for Alice","author":"Podcaster","image_url":"https://example.com/alice-cover.png"}
```

| Field | Required | Notes |
| --- | --- | --- |
| `id` | yes | URL slug: 2–32 chars, lowercase letter first, then lowercase letters, digits, or hyphens; must not end with a hyphen (`-`) or contain consecutive hyphens (`--`). Reserved names (`v1`, `audio`, `podcast`, …) are rejected. |
| `title` | yes | 1–200 characters |
| `description` | no | Optional RSS description |
| `author` | no | Optional `itunes:author` (defaults to `PODCAST_AUTHOR`) |
| `image_url` | no | Optional custom podcast cover icon (`http://` or `https://` URL, or an inline `data:image/png;base64,...` / `data:image/jpeg;base64,...` data URI up to 5 MiB). Inline data URIs are decoded, stored in object storage, and rewritten to `{PUBLIC_BASE_URL}/p/{id}/cover.png`. When omitted, the RSS feed uses `/p/{id}/cover.png`. |

`403` if called with a scoped submit key. `409` if `id` already exists. `422` invalid slug or fields.

**Response `201 Created`**

```json
{
  "id": "alice",
  "title": "Alice Briefing",
  "description": "Private updates for Alice",
  "author": "Podcaster",
  "image_url": "https://example.com/alice-cover.png",
  "username": "alice",
  "password": "<random>",
  "token": "<random>",
  "submit_key": "<random>",
  "feed_url": "https://host/p/alice/podcast.xml",
  "subscribe_url": "https://alice:<password>@host/p/alice/podcast.xml",
  "created_at": "2026-09-26T13:30:00Z"
}
```

### `GET /v1/podcasts`

List all private shows (including listener secrets and `submit_key`s — requires the **Super Key**). Listener passwords and submit keys are stored on the show document in SQLite/Firestore.

```json
{
  "podcasts": [
    {
      "id": "alice",
      "title": "Alice Briefing",
      "description": "Private updates for Alice",
      "author": "Podcaster",
      "image_url": "https://example.com/alice-cover.png",
      "username": "alice",
      "password": "<random>",
      "token": "<random>",
      "submit_key": "<random>",
      "feed_url": "https://host/p/alice/podcast.xml",
      "subscribe_url": "https://alice:<password>@host/p/alice/podcast.xml",
      "created_at": "2026-09-26T13:30:00Z"
    }
  ]
}
```

### `GET /v1/podcasts/{id}`

Returns the single show object (same payload as `POST /v1/podcasts`, including `"submit_key"` and optional `"image_url"`). Accessible with the **Super Key** or `{id}`'s **Per-User Submit Key**. `404` unknown id, `403` for other scoped keys.

### `PATCH /v1/podcasts/{id}` (alias `PUT /v1/podcasts/{id}`)

Update an existing show's metadata (`title`, `description`, `author`, and/or `image_url`). Callable with the **Super Key** (`ADMIN_API_KEY`, or `AGENT_API_KEY` when `ADMIN_API_KEY` is unset) or `{id}`'s **Per-User Submit Key** (`submit_key`). Only fields included in the JSON body are updated.

```http
PATCH /v1/podcasts/alice HTTP/1.1
Authorization: Bearer <SUPER_KEY_OR_ALICE_SUBMIT_KEY>
Content-Type: application/json

{
  "title": "Alice Executive Briefing",
  "description": "Daily morning and evening updates for Alice",
  "author": "Alice Agent",
  "image_url": "https://example.com/alice-new-icon.png"
}
```

| Field | Required | Notes |
| --- | --- | --- |
| `title` | no | 1–200 characters (non-empty when provided) |
| `description` | no | Updated RSS description (pass `""` to clear) |
| `author` | no | Updated `itunes:author` (pass `""` to revert to `PODCAST_AUTHOR`) |
| `image_url` | no | Updated custom podcast cover icon (`http://` or `https://` URL, inline `data:image/png;base64,...` / `data:image/jpeg;base64,...` up to 5 MiB, or `""` to revert to `/p/{id}/cover.png`) |

**Response `200 OK`**

```json
{
  "id": "alice",
  "title": "Alice Executive Briefing",
  "description": "Daily morning and evening updates for Alice",
  "author": "Alice Agent",
  "image_url": "https://example.com/alice-new-icon.png",
  "username": "alice",
  "password": "<password>",
  "token": "<token>",
  "submit_key": "<submit-key>",
  "feed_url": "https://host/p/alice/podcast.xml",
  "subscribe_url": "https://alice:<password>@host/p/alice/podcast.xml",
  "created_at": "2026-09-26T13:30:00Z"
}
```

`400` malformed JSON, `403` if authenticated with a key not authorized for `{id}`, `404` if show `{id}` does not exist, `422` validation error.

### `POST /v1/podcasts/{id}/rotate`

Rotate or update credentials for show `{id}`. Accessible with the **Super Key** or `{id}`'s **Per-User Submit Key** (`submit_key` can rotate `{id}`'s listening credentials; rotating `submit_key` requires the Super Key or is supported per options).

By default (with an empty body or `{}`), rotates the listener `password` and `token`, immediately invalidating the previous `subscribe_url` and `?token=` enclosure links. Accepts optional JSON:

```http
POST /v1/podcasts/alice/rotate HTTP/1.1
Authorization: Bearer <SUPER_KEY_OR_ALICE_SUBMIT_KEY>
Content-Type: application/json

{"rotate_listener": true, "rotate_submit_key": false, "password": "...", "token": "..."}
```

| Field | Required | Notes |
| --- | --- | --- |
| `rotate_listener` | no | Defaults to `true` when no other field is set. Generates a new random listener `password` and `token`. |
| `rotate_submit_key` | no | When `true`, generates a new random `submit_key` for publishing to `{id}`. |
| `password` | no | Optional custom listener Basic Auth password (overrides random generation). |
| `token` | no | Optional custom listener feed `?token=` (overrides random generation). |

**Response `200 OK`**

```json
{
  "id": "alice",
  "title": "Alice Briefing",
  "description": "Private updates for Alice",
  "author": "Podcaster",
  "image_url": "https://example.com/alice-cover.png",
  "username": "alice",
  "password": "<new-password>",
  "token": "<new-token>",
  "submit_key": "<submit-key>",
  "feed_url": "https://host/p/alice/podcast.xml",
  "subscribe_url": "https://alice:<new-password>@host/p/alice/podcast.xml",
  "created_at": "2026-09-26T13:30:00Z"
}
```

`404` if show `{id}` does not exist. `403` if authenticated with a key not authorized for `{id}`.

### `POST /v1/podcasts/{id}/episodes`

Same body as `POST /v1/episodes` (including optional `description`, `image_url`, and `chapters`), scoped to that show. Equivalent to sending `"podcast_id":"{id}"` on the default ingest path (returns `422` if body `podcast_id` is set to a different slug).

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
  "description": "Key links and summary for today's briefing.",
  "voice_id": "af_heart",
  "category": "Daily Briefing",
  "image_url": "https://example.com/episodes/sept-26.png",
  "chapters": [
    {
      "start_seconds": 0,
      "title": "Intro",
      "url": "https://example.com/intro",
      "image_url": "https://example.com/intro.png"
    },
    {
      "start_seconds": 15.5,
      "title": "Top Stories"
    }
  ],
  "podcast_id": "alice"
}
```

| Field | Required | Notes |
| --- | --- | --- |
| `title` | yes | 1–200 characters (override with `MAX_TITLE_LENGTH`) |
| `content` | yes | 10–100 000 characters (`MIN_CONTENT_LENGTH`, `MAX_CONTENT_LENGTH`), valid UTF-8. Spoken script; SSML tags and Markdown formatting (headings, bold/italic, code fences, links, raw URLs, bullet markers) are cleaned and split into paragraph/sentence chunks before TTS. |
| `description` | no | Optional episode show notes / summary (distinct from the `content` TTS script), up to `MAX_CONTENT_LENGTH` characters. |
| `voice_id` | no | Defaults to `DEFAULT_VOICE` (`af_heart`). Built-in Kokoro v1.0 English voices include US female (`af_heart`, `af_alloy`, `af_aoede`, `af_bella`, `af_jessica`, `af_kore`, `af_nicole`, `af_nova`, `af_river`, `af_sarah`, `af_sky`), US male (`am_adam`, `am_echo`, `am_eric`, `am_fenrir`, `am_liam`, `am_michael`, `am_onyx`, `am_puck`, `am_santa`), UK female (`bf_alice`, `bf_emma`, `bf_isabella`, `bf_lily`), and UK male (`bm_daniel`, `bm_fable`, `bm_george`, `bm_lewis`). Rejected if `VOICE_ALLOWLIST` is set and the id is not listed. |
| `category` | no | Shown in the RSS `<category>` and description prefix. |
| `image_url` | no | Optional per-episode artwork (`http://` or `https://` URL, or inline `data:image/png;base64,...` / `data:image/jpeg;base64,...` up to 5 MiB), rendered as `<itunes:image>` on the RSS `<item>`. Inline data URIs are stored in object storage and rewritten to `/episodes/{id}/cover.png` (or `/p/{podcast_id}/episodes/{id}/cover.png`). |
| `chapters` | no | Optional array of chapter markers (`[{"start_seconds": 0, "title": "Intro", "url": "https://...", "image_url": "https://..."}]`). Each chapter requires `start_seconds >= 0` and non-empty `title` (`<= MAX_TITLE_LENGTH`), with optional `http://` or `https://` `url` and `image_url`. |
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

List recent episodes (newest first). Query: `status`, `podcast_id`, `only_default=true` (filter to default-show episodes with empty `podcast_id`), `limit` (default 50, max 100), `offset`.

```json
{
  "episodes": [
    {
      "episode_id": "ep_8f9a2b1c0d1e2f3a",
      "podcast_id": "alice",
      "title": "Morning Briefing - Sept 26, 2026",
      "description": "Key links and summary for today's briefing.",
      "status": "READY",
      "category": "Daily Briefing",
      "image_url": "https://example.com/episodes/sept-26.png",
      "chapters": [
        {
          "start_seconds": 0,
          "title": "Intro",
          "url": "https://example.com/intro",
          "image_url": "https://example.com/intro.png"
        }
      ],
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
  "description": "Key links and summary for today's briefing.",
  "status": "READY",
  "category": "Daily Briefing",
  "image_url": "https://example.com/episodes/sept-26.png",
  "chapters": [
    {
      "start_seconds": 0,
      "title": "Intro",
      "url": "https://example.com/intro",
      "image_url": "https://example.com/intro.png"
    },
    {
      "start_seconds": 15.5,
      "title": "Top Stories"
    }
  ],
  "voice_id": "af_heart",
  "duration_seconds": 42.1,
  "file_size_bytes": 675840,
  "error_message": "",
  "created_at": "2026-09-26T13:30:00Z",
  "published_at": "2026-09-26T13:30:08Z"
}
```

`status` is one of `QUEUED`, `PROCESSING`, `READY`, `FAILED`. (`description`, `image_url`, and `chapters` are omitted when empty.)

### `PATCH /v1/episodes/{id}` (alias `PUT /v1/episodes/{id}`)

Update an existing episode's metadata (`title`, `description`, `category`, `image_url`, and/or `chapters`) without re-running audio synthesis. Scoped by the caller's key tier (Super Key can update any episode; Default-Feed Submit Key can update default-feed episodes; Per-User Submit Key can update episodes belonging to its show `{id}`). Only fields included in the JSON body are updated.

```http
PATCH /v1/episodes/ep_8f9a2b1c0d1e2f3a HTTP/1.1
Authorization: Bearer <SUPER_KEY_OR_SUBMIT_KEY>
Content-Type: application/json

{
  "title": "Morning Briefing - Sept 26, 2026 (Updated)",
  "description": "Updated show notes with chapter timestamps.",
  "category": "Daily Briefing",
  "image_url": "https://example.com/episodes/sept-26-v2.png",
  "chapters": [
    {"start_seconds": 0, "title": "Intro", "url": "https://example.com/intro"},
    {"start_seconds": 15.5, "title": "Top Stories"}
  ]
}
```

| Field | Required | Notes |
| --- | --- | --- |
| `title` | no | 1–200 characters (non-empty when provided) |
| `description` | no | Episode show notes / summary, up to `MAX_CONTENT_LENGTH` characters (pass `""` to clear) |
| `category` | no | Episode category (pass `""` to clear) |
| `image_url` | no | Per-episode artwork (`http://` or `https://` URL, inline `data:image/png;base64,...` / `data:image/jpeg;base64,...` up to 5 MiB, or `""` to clear) |
| `chapters` | no | Array of chapter markers (pass `[]` to clear) |

**Response `200 OK`**

Returns the updated episode object (same payload shape as `GET /v1/episodes/{id}`).

`400` malformed JSON, `403` if authenticated with a key not authorized for the episode's show, `404` if the episode does not exist, `422` validation error.

### `GET /p/{id}/podcast.xml` (alias `GET /p/{id}/feed.xml`)

That show's RSS. Only its `READY` episodes. Auth: that show's Basic or `?token=`.

Enclosure URLs are `/p/{id}/audio/{episode_id}.mp3?token=...`. Channel cover `<itunes:image>` uses the show's custom `image_url` if set, otherwise `/p/{id}/cover.png`.

### `GET /podcast.xml` (alias `GET /feed.xml`)

Default show RSS. Only episodes **without** a `podcast_id`. Auth: `FEED_USERNAME` / `FEED_PASSWORD` (Basic) or `?token=` (`FEED_TOKEN` or derived token).

Notable channel tags:

- `itunes:author`, `itunes:image` (custom show `image_url` when set, or `/cover.png` / `/p/{id}/cover.png`), `itunes:category`, `itunes:explicit`
- `itunes:block` = `yes` (private)
- `atom:link rel="self"`

Notable `<item>` tags:

- `enclosure url length type`, `itunes:duration`, `guid`
- `<description>` and `<itunes:summary>`: Uses the episode's `description` (show notes) when set, falling back to `content` (`script_text`) or `title`, prefixed with `category: ` when `category` is non-empty.
- `<content:encoded>`: Full episode show notes (`description` when set, plus the full `content` script/transcript).
- `<itunes:image href="..."/>`: Per-episode artwork when the episode has a custom `image_url`.
- `<psc:chapters version="1.2">` (`xmlns:psc="http://podlove.org/simple-chapters"`): Inline Podlove Simple Chapters (`<psc:chapter start="HH:MM:SS" title="..." href="..." image="..."/>`) rendered when the episode has `chapters`.
- `<podcast:chapters url="..." type="application/json+chapters"/>` (`xmlns:podcast="https://podcastindex.org/namespace/1.0"`): Points to `/episodes/{id}/chapters.json?token=...` (default feed) or `/p/{id}/episodes/{ep}/chapters.json?token=...` (per-user show) when the episode has `chapters`.

### `GET /episodes/{id}/chapters.json` and `GET /p/{id}/episodes/{ep}/chapters.json`

Serves an episode's chapter markers in the Podcasting 2.0 JSON Chapters format (`Content-Type: application/json+chapters; charset=utf-8`). `HEAD` is supported.

- `GET /episodes/{id}/chapters.json`: Default-show episodes only (`podcast_id == ""`). Auth: default show's Basic Auth (`FEED_USERNAME` / `FEED_PASSWORD`) or `?token=`. Returns `404` if the episode belongs to a per-user show.
- `GET /p/{id}/episodes/{ep}/chapters.json`: Scoped to show `{id}`. Auth: show `{id}`'s Basic Auth or `?token=`. Returns `404` if the episode does not exist or belongs to a different show.

**Response `200 OK`**

```json
{
  "version": "1.2.0",
  "chapters": [
    {
      "startTime": 0,
      "title": "Intro",
      "url": "https://example.com/intro",
      "img": "https://example.com/intro.png"
    },
    {
      "startTime": 15.5,
      "title": "Top Stories"
    }
  ]
}
```

### `GET /p/{id}/audio/{episode_id}.mp3`

That show's enclosure. Auth: that show's Basic or `?token=`. Returns **404** if the episode belongs to a different show (including the default show). `HEAD` is supported.

On GCS, a successful auth **307**s to a short-lived signed object URL. If signing is unavailable the service streams the bytes itself.

### `GET /audio/{episode_id}.mp3`

Default-show enclosure only (empty `podcast_id`). Same 307 behaviour. Per-user episodes are **404** here even with default credentials.

### `GET /cover.png`, `GET /p/{id}/cover.png`, `GET /episodes/{id}/cover.png`, and `GET /p/{id}/episodes/{ep}/cover.png`

Podcast and episode cover artwork (`image/png` or `image/jpeg`). No auth (podcast clients fetch artwork without credentials).

- `GET /cover.png`: Default show cover (generated 1400×1400 PNG, or overridden with `PODCAST_IMAGE_FILE`).
- `GET /p/{id}/cover.png`: Show `{id}`'s stored inline cover if uploaded via `data:image/(png|jpeg);base64,...`, falling back to the default cover.
- `GET /episodes/{id}/cover.png` and `GET /p/{id}/episodes/{ep}/cover.png`: Episode `{id}` / `{ep}`'s stored inline cover if uploaded via `data:image/(png|jpeg);base64,...`, falling back to the show cover (`404` if the episode does not exist or belongs to a different show).

### `GET /healthz` / `GET /readyz`

Liveness (`/healthz`) always returns `200 {"status":"ok"}`. Readiness (`/readyz`) pings the metadata store and returns `200 {"status":"ready"}` or `503 {"status":"unready","error":"..."}`. From the public internet on Cloud Run, `/healthz` may be intercepted by Google's frontend (HTML 404); use `/readyz`.

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
| `image_url` | no | Custom show cover icon (`http://`, `https://`, or `data:image/(png\|jpeg);base64,...`) |

Requires the Super Key. Returns `id`, `title`, `description`, `author`, `image_url` (if set), `username`, `password`, `token`, `submit_key`, `feed_url`, `subscribe_url`, `created_at`, and `message`.

### Tool `update_podcast`

| Input | Required | Description |
| --- | --- | --- |
| `podcast_id` (or `id`) | yes | Show slug (`alice`) |
| `title` | no | Updated show title (1–200 chars) |
| `description` | no | Updated RSS description (pass `""` to clear) |
| `author` | no | Updated `itunes:author` (pass `""` to clear) |
| `image_url` | no | Updated custom podcast cover icon (`http://`, `https://`, `data:image/(png\|jpeg);base64,...`, or `""` to clear) |

Accessible with the Super Key or that show's `submit_key`. Returns the updated show object (`id`, `title`, `description`, `author`, `image_url`, `username`, `password`, `token`, `submit_key`, `feed_url`, `subscribe_url`, `created_at`, and `message`).

### Tool `list_podcasts`

Requires the Super Key. Takes no required parameters. Returns `{"podcasts": [...]}` with each show's metadata (including `image_url` when set), credentials (`username`, `password`, `token`, `submit_key`), `feed_url`, `subscribe_url`, and `created_at`.

### Tool `get_podcast`

| Input | Required | Description |
| --- | --- | --- |
| `podcast_id` (or `id`) | yes | Show slug (`alice`) |

Accessible with the Super Key or that show's `submit_key`. Returns the show's metadata (including `image_url` when set), credentials (`username`, `password`, `token`, `submit_key`), `feed_url`, `subscribe_url`, and `created_at`.

### Tool `rotate_podcast_credentials`

| Input | Required | Description |
| --- | --- | --- |
| `podcast_id` (or `id`) | yes | Show slug (`alice`) |
| `rotate_listener` | no | Rotate listener `password` and `token` (defaults to `true` when no other option is set) |
| `rotate_submit_key` | no | Rotate the show's publisher `submit_key` (default `false`) |
| `password` | no | Optional custom listener password |
| `token` | no | Optional custom listener `?token=` |

Accessible with the Super Key or that show's `submit_key`. Returns the updated show object with the new `password`, `token`, `submit_key`, `feed_url`, `subscribe_url`, `created_at`, and `message`.

### Tool `publish_agent_update`

| Input | Required | Description |
| --- | --- | --- |
| `title` | yes | Episode title |
| `content` | yes | Plain-text script spoken by TTS |
| `description` | no | Optional episode show notes / summary (distinct from the `content` script) |
| `category` | no | e.g. `Daily Briefing` |
| `voice_id` | no | Kokoro voice id (e.g. `af_heart`, `af_bella`, `am_adam`, `am_fenrir`, `am_michael`, `bf_emma`, `bm_george`) |
| `image_url` | no | Optional per-episode artwork (`http://`, `https://`, or `data:image/(png\|jpeg);base64,...`) |
| `chapters` | no | Optional chapter markers (`[{"start_seconds": 0, "title": "Intro", "url": "https://...", "image_url": "https://..."}]`) |
| `podcast_id` | no | Show slug. Empty publishes to the default feed (or defaults to the scoped show when authenticated with a per-user `submit_key`). |

**Output**

```json
{
  "status": "QUEUED",
  "episode_id": "ep_8f9a2b1c0d1e2f3a",
  "podcast_id": "alice",
  "created_at": "2026-09-26T13:30:00Z",
  "message": "Batch job initialized. Episode will appear in the feed once synthesis completes."
}
```

### Tool `update_episode`

| Input | Required | Description |
| --- | --- | --- |
| `episode_id` (or `id`) | yes | Episode identifier (`ep_...`) |
| `title` | no | Updated episode title |
| `description` | no | Updated episode show notes / summary (pass `""` to clear) |
| `category` | no | Updated category (pass `""` to clear) |
| `image_url` | no | Updated per-episode artwork (`http://`, `https://`, `data:image/(png\|jpeg);base64,...`, or `""` to clear) |
| `chapters` | no | Updated chapter markers array (pass `[]` to clear) |

Scoped by the caller's key tier (Super Key, Default-Feed Submit Key, or Per-User Submit Key). Returns the updated episode metadata (`episode_id`, `podcast_id`, `title`, `description`, `status`, `category`, `image_url`, `chapters`, `voice_id`, `duration_seconds`, `file_size_bytes`, `error_message`, `created_at`, `published_at`).

### Tool `get_episode_status`

Input: `episode_id`. Returns `episode_id`, `podcast_id` (if set), `title`, `description` (if set), `status`, `category` (if set), `image_url` (if set), `chapters` (if set), `voice_id` (if set), `duration_seconds` (if set), `file_size_bytes` (if set), `error_message` (if set), `created_at`, and `published_at` (if set).

### Tool `list_episodes`

| Input | Required | Description |
| --- | --- | --- |
| `status` | no | Filter by status (`QUEUED`, `PROCESSING`, `READY`, `FAILED`) |
| `podcast_id` | no | Filter by show slug |
| `only_default` | no | Filter to default-show episodes (`podcast_id == ""`) |
| `limit` | no | Page size (default 50, max 100) |
| `offset` | no | Pagination offset |

Returns `{"episodes": [...]}` (each item includes `description`, `image_url`, and `chapters` when set).

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
# create a private show (requires ADMIN_API_KEY, or AGENT_API_KEY if ADMIN_API_KEY is unset)
curl -sS -X POST "$BASE/v1/podcasts" \
  -H "Authorization: Bearer $ADMIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"id":"alice","title":"Alice Briefing","description":"Private updates for Alice","image_url":"https://example.com/alice-cover.png"}'

# update show title, description, or custom icon (using the show's submit_key or the super key)
curl -sS -X PATCH "$BASE/v1/podcasts/alice" \
  -H "Authorization: Bearer $ALICE_SUBMIT_KEY" \
  -H "Content-Type: application/json" \
  -d '{"title":"Alice Executive Briefing","image_url":"https://example.com/alice-new-icon.png"}'

# publish to that show with show notes, artwork, and chapters
curl -sS -X POST "$BASE/v1/podcasts/alice/episodes" \
  -H "Authorization: Bearer $ALICE_SUBMIT_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "title":"Morning Briefing",
    "content":"Good morning. Here are your top updates for today.",
    "description":"Show notes and links for today.",
    "image_url":"https://example.com/episodes/morning.png",
    "chapters":[{"start_seconds":0,"title":"Intro"},{"start_seconds":12,"title":"Headlines"}]
  }'

# update an existing episode's metadata or chapters
curl -sS -X PATCH "$BASE/v1/episodes/ep_..." \
  -H "Authorization: Bearer $ALICE_SUBMIT_KEY" \
  -H "Content-Type: application/json" \
  -d '{"description":"Updated show notes","chapters":[{"start_seconds":0,"title":"Welcome"}]}'

# poll
curl -sS "$BASE/v1/episodes/ep_..." -H "Authorization: Bearer $ALICE_SUBMIT_KEY"

# rotate listener credentials for alice (invalidates old password and ?token=)
curl -sS -X POST "$BASE/v1/podcasts/alice/rotate" \
  -H "Authorization: Bearer $ALICE_SUBMIT_KEY" \
  -H "Content-Type: application/json" \
  -d '{}'

# that show's feed and JSON chapters (use the password from create/rotate)
curl -sS -u "alice:$ALICE_PASSWORD" "$BASE/p/alice/podcast.xml"
curl -sS -u "alice:$ALICE_PASSWORD" "$BASE/p/alice/episodes/ep_.../chapters.json"

# default show (env FEED_*)
curl -sS -u "$FEED_USERNAME:$FEED_PASSWORD" "$BASE/podcast.xml"
```
