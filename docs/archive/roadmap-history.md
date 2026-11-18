# Roadmap History (`podcaster`)

Archived shipped releases moved out of [`../roadmap.md`](../roadmap.md) on `2026-11-18`.

---

## v0.1.0 — Multi-Show Kokoro TTS Platform and MCP Server

- Shipped pure-Go (`CGO_ENABLED=0`, distroless) personal audio podcast platform (`https://boris.tvaroska.sk`) with Kokoro-82M (`sherpa-onnx`) TTS synthesis, `-16 LUFS` 24 kHz mono MP3 mastering, multi-show isolation with 3-tier API keys (`ADMIN_API_KEY`, `AGENT_API_KEY`, per-show `submit_key`), SQLite & Firestore store parity, CAS worker status lifecycle, Cloud Run Job reconciliation, RSS + `<psc:chapters>` / `chapters.json`, and initial MCP server.
- Added Trivy dependency upgrades (`go-jose/v4`, `golang.org/x/net`), `CRITICAL.md`, `DECISIONS.md`, `hygiene_test.go`, and `gofmt -s` CI enforcement in `deploy/cloudbuild.yaml`.
