# Podcaster Roadmap (`docs/roadmap.md`)

**Last Updated:** 2026-11-15  
**Architecture & Specs:** [`architecture.md`](architecture.md) · [`api.md`](api.md) · [`spec.md`](spec.md) · [`../DECISIONS.md`](../DECISIONS.md)

---

## P1 — ML/AI, Quantization & Systems Pronunciation Lexicon (`internal/tts/preprocess.go`)

Kokoro-82M (`sherpa-onnx`) handles standard English prose well, but mixed-case AI lab names, quantization formats, and developer tooling identifiers are currently either slurred as unnatural phonetic guesses (`OpenAI`, `vLLM`, `Qwen`, `pgvector`) or missed by the strict uppercase initialism regex (`\b([A-Z]{2,6})(s?)\b`).

### Milestones

- [ ] **P1.1 — Curated ML/AI & Infra Phonetics Table (`internal/tts/preprocess.go`):** Expand `phoneticOverrides` and token normalization rules in [`../internal/tts/preprocess.go`](../internal/tts/preprocess.go) with a structured technical pronunciation lexicon across four categories:
  1. **AI Labs & Frontier Model Families:** `OpenAI` (`"Open A I"`), `xAI` (`"X A I"`), `GenAI` (`"Gen A I"`), `DeepSeek` (`"Deep Seek"`), `DeepMind` (`"Deep Mind"`), `Qwen` (`"Kwen"`), `Nemotron` (`"Nemo-tron"`), `Mixtral` (`"Mix-trahl"`), `NotebookLM` (`"Notebook L M"`), plus a camelCase `*AI` suffix rule (`[A-Z][a-z]+AI\b` -> split before `A I` for `CrewAI`, `MistralAI`, `TogetherAI`, `ScaleAI`, `AssemblyAI`).
  2. **Architecture, Fine-Tuning & Quantization Terms:** `LoRA` (`"Laura"`), `QLoRA` (`"Q Laura"`), `DoRA` (`"Dora"`), `RoPE` (`"rope"`), `MoE` (`"M O E"`), `SwiGLU` (`"Swig-loo"`), `KV` (`"K V"`), `RLHF` / `RLAIF` / `DPO` / `GRPO` (spelled initialisms), `FP16` / `FP8` / `FP4` (`"F P sixteen"`, `"F P eight"`, `"F P four"`), `BF16` (`"B F sixteen"`), `NVFP4` (`"N V F P four"`), `MXFP8` (`"M X F P eight"`), `INT8` / `INT4` (`"int eight"`, `"int four"`), `GGUF` (`"G G U F"`), `AWQ`, `GPTQ`.
  3. **Inference Runtimes, Frameworks & Data Systems:** `vLLM` (`"V L L M"`), `SGLang` (`"S G Lang"`), `TensorRT` (`"Tensor R T"`), `LlamaIndex` (`"Llama Index"`), `LangChain` (`"Lang Chain"`), `LangGraph` (`"Lang Graph"`), `Pydantic` (`"Pie-dantic"`), `PyTorch` (`"Pie-torch"`), `PyMC` (`"Pie M C"`), `NumPy` (`"Num-pie"`), `SciPy` (`"Sigh-pie"`), `FastAPI` (`"Fast A P I"`), `GraphQL` (`"Graph Q L"`), `PostgreSQL` (`"Postgres Q L"`), `SQLite` (`"S Q Lite"`), `pgvector` (`"P G vector"`), `DuckDB` (`"Duck D B"`), `ClickHouse` (`"Click House"`).
  4. **Publishing, Cloud & Systems Terms:** `arXiv` (`"archive"`), `a16z` (`"A sixteen Z"`), `Substack` (`"Sub-stack"`), `GitHub` (`"Git Hub"`), `GitOps` (`"Git Ops"`), `FinOps` (`"Fin Ops"`), `DevOps` (`"Dev Ops"`), `MLOps` (`"M L Ops"`), `OAuth` (`"Oh Auth"`), `gRPC` (`"G R P C"`), `mTLS` (`"M T L S"`), `k8s` (`"Kubernetes"`), `k3s` (`"K three S"`), `NVLink` (`"N V Link"`), `CUDA` (`"Cooda"`), `ONNX` (`"Onyx"`).
- [ ] **P1.2 — Plural, Possessive, Boundary-Safe Matching & `510`-Phoneme Chunk Budget:** Handle plural and possessive suffixes cleanly (`LLMs` -> `"L L Ms"`, `GPUs` -> `"G P Us"`, `LoRAs` -> `"Lauras"`, `OpenAI's` -> `"Open A I's"`) without corrupting inline URLs already stripped earlier in `Preprocess` or ordinary English substrings, and enforce `maxChunkRunes` *after* phonetic expansion so dense acronym-heavy sentences never exceed Kokoro-82M's `510`-phoneme ONNX input ceiling.
- [ ] **P1.3 — Optional External Phonetics Overlay (`TTS_PHONETICS_FILE`) & Table Tests:** Support an optional JSON/env lexicon file merged on top of compiled defaults so new terms can be tuned without rebuilding container images, backed by comprehensive table-driven tests in `internal/tts/preprocess_test.go`.

---

## P2 — Optional Exact Audio-Sample Chapter Anchors (`cmd/worker`)

- [ ] **P2.1 — Inline `[CHAPTER: <Title>]` Sample-Accurate Timestamp Resolution:** Episode publishers can already submit arbitrary per-source or per-item `chapters` (`[]episode.Chapter{StartSeconds, Title}`) via `POST /v1/episodes` and `PATCH /v1/episodes/{id}` ([`api.md`](api.md)). To eliminate word-count WPM estimation drift on 30+ minute episodes, optionally parse inline `[CHAPTER: <Title>]` markers during chunk splitting in `internal/worker`, strip the marker before Kokoro synthesis, and compute exact `start_seconds` from cumulative synthesized WAV sample frames prior to `ffmpeg` `-16 LUFS` mastering.

---

## P3 — MCP `2026-07-28` Upgrade (`internal/mcp`)

- [ ] **P3.1 — Stateless Core & `2026-07-28` Discovery (`internal/mcp`):** Migrate `/mcp` from session-bound handshakes to the stateless `2026-07-28` protocol (`_meta`, `MCP-Protocol-Version` header, `server/discover`, cacheable tool/resource lists) so Cloud Run scales without session pinning.
- [ ] **P3.2 — Async Synthesis via `SEP-2663` Tasks (`io.modelcontextprotocol/tasks`):** Return an MCP Task handle from episode creation so clients track or cancel async Kokoro-82M + `ffmpeg` Cloud Run Jobs (`tasks/get`, `tasks/update`, `tasks/cancel`) instead of polling `get_episode`.
- [ ] **P3.3 — Episode & Chapter Resources (`podcast://shows/{id}/episodes/{ep}`):** Expose transcripts, `chapters.json`, and episode metadata as URI-addressed MCP Resources so AI clients can attach show context directly.
