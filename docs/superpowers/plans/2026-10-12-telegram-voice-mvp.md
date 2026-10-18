# Telegram Voice MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable authorized Telegram voice notes to be transcribed locally, answered by the existing secretary, and returned as text plus spoken audio.

**Architecture:** Keep `lab-secretary` as the conversation owner and Telegram adapter. Add a channel-neutral speech client for local Whisper and Piper HTTP services, plus an in-memory FFmpeg converter. Deploy pinned CPU-only speech services as isolated loopback systemd units and expose bounded voice metrics through the existing secretary HTTP listener for Prometheus.

**Tech Stack:** Go 1.23, Telegram Bot API, `whisper.cpp` v1.9.5, Piper `piper-tts` 1.8.0, FFmpeg, systemd, Ansible, Prometheus.

**Spec:** `docs/superpowers/specs/2026-10-12-telegram-voice-mvp-design.md`

## Global Constraints

- Process complete voice notes; do not implement live calls or partial-audio streaming.
- Run Whisper, Piper, FFmpeg, and the secretary on the lab host; Whisper and Piper bind only to loopback.
- Preserve the Telegram user allowlist and reject unauthorized updates before media download or inference.
- Enforce the Telegram cloud download cap of 20 MB and a configured voice-duration ceiling of 120 seconds.
- Send written output before synthesis; a TTS or audio-delivery failure must not retract the text answer.
- Do not log, persist, or add raw audio, transcripts, prompts, chat IDs, user IDs, filenames, file URLs, or bot tokens to metrics.
- Use bounded metric labels only: phase (`stt`, `llm`, `tts`, `telegram_send`, `other`) and result (`ok`, `error`, `cancelled`, `other`).
- Cap Piper response bodies at Telegram's 50 MB voice-upload limit; make the client cap injectable so tests can exercise rejection without allocating large fixtures.
- Keep transient audio in memory where possible; run Whisper with an isolated private temporary directory and remove converted files on every path.
- Keep model weights and built executables out of Git; commit pinned URLs, revisions, SHA-256 values, license notes, deployment files, and validation evidence.
- Pin Whisper source to v1.9.5 and model `ggml-small-q5_1.bin` at revision `5359861c739e955e79d9a303bcbc70fb988958b1`, SHA-256 `ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb`.
- Pin Piper `piper-tts` 1.8.0 and voice `es_ES-sharvard-medium.onnx` from `rhasspy/piper-voices` v1.0.0, SHA-256 `40febfb1679c69a4505ff311dc136e121e3419a13a290ef264fdf43ddedd0fb1`; pin its required `.onnx.json` config at the same revision, SHA-256 `7438c9b699c72b0c3388dae1b68d3f364dc66a2150fe554a1c11f03372957b2c`. Record engine GPL-3.0-or-later and voice MIT licenses separately.
- Preserve the current 128 MB memory cap on `lab-secretary`; speech models load in their own constrained systemd services.

## Review Focus

- **Unauthorized voice update:** must be rejected before `getFile` or either inference endpoint; test in Task 2.
- **Forged, missing, oversized, or over-duration media metadata/body:** must fail closed without inference and clean transient data; tests in Task 2.
- **Empty/no-speech or malformed Whisper response:** must ask for a clearer note and must not call the LLM; tests in Tasks 1 and 2.
- **Whisper, Piper, FFmpeg, or Telegram API errors:** must return a safe message, preserve any already-sent text, and never include the bot token in error text; tests in Tasks 1 and 2.
- **Metrics exposition:** must contain only bounded phase/result labels and no user- or message-derived values; tests in Task 3.

## File Structure

- `internal/secretary/speech.go` — speech and audio-conversion interfaces plus HTTP clients for Whisper and Piper.
- `internal/secretary/speech_test.go` — HTTP contract, request limits, parsing, timeout, and FFmpeg pipe tests using local fakes.
- `internal/secretary/voice_metrics.go` — private Prometheus registry and bounded voice metrics.
- `internal/secretary/voice_metrics_test.go` — exposition and label-safety tests.
- `internal/secretary/telegram.go` — Telegram voice schema, media download/upload, allowlisted voice flow, and text-first delivery.
- `internal/secretary/telegram_test.go` — update, authorization, file-limit, flow, fallback, and token-redaction tests.
- `internal/secretary/service.go` and `internal/secretary/service_test.go` — mount `/metrics` on the existing loopback HTTP handler and test it.
- `cmd/lab-secretary/main.go` — wire voice configuration, local speech client, and FFmpeg encoder without changing secret handling.
- `group_vars/all.yml` and `playbooks/deploy-secretary.yml` — tracked feature switch, pinned runtime defaults, and role inclusion.
- `roles/lab_speech/defaults/main.yml` — ports, paths, model pins, CPU limits, and service defaults.
- `roles/lab_speech/tasks/main.yml` and `roles/lab_speech/handlers/main.yml` — package installation, source build, model download/hash checks, systemd units, health checks, and disable/rollback behavior.
- `roles/observability/tasks/main.yml` — add `lab-secretary` as a Prometheus target when the secretary is enabled.
- `docs/secretary-operation.md` and `docs/system-usage.md` — operator controls, formats, privacy, license, limits, and rollback.
- `scripts/smoke-secretary-voice.py` — local HTTP contract smoke test that generates a short Spanish sample, transcribes it, and verifies Piper audio output without retaining sample media.

---

### Task 1: Add Local Speech Client and In-Memory Audio Conversion

**Files:**
- Create: `internal/secretary/speech.go`
- Create: `internal/secretary/speech_test.go`

**Interfaces:**
- Produces `type SpeechProcessor interface { Transcribe(context.Context, []byte) (string, error); Synthesize(context.Context, string) ([]byte, error) }`.
- Produces `type AudioConverter interface { ToWAV(context.Context, []byte) ([]byte, error); ToOGGOpus(context.Context, []byte) ([]byte, error) }`.
- Produces `NewHTTPVoiceClient(whisperURL, piperURL string, client *http.Client) *HTTPVoiceClient` implementing `SpeechProcessor`.
- Produces `FFmpegAudioConverter{Binary string}` implementing `AudioConverter`; both conversions use stdin/stdout pipes and create no application-owned media files.

- [ ] **Step 1: Write failing Whisper and Piper client tests**

Add `TestHTTPVoiceClientTranscribePostsSpanishWAVAndParsesJSON`, asserting multipart field `file=voice.wav`, field `language=es`, JSON response text extraction, and no user-derived filename. Add `TestHTTPVoiceClientSynthesizePostsJSONAndReturnsWAV`, asserting `POST /synthesize`, JSON text body, and WAV response bytes. Add table cases for malformed/empty responses, non-200 status, request cancellation, and configured maximum response size.

- [ ] **Step 2: Run the new tests and verify they fail**

Run: `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 go test ./internal/secretary -run 'TestHTTPVoiceClient' -count=1`
Expected: FAIL because the speech client and interfaces do not exist.

- [ ] **Step 3: Implement `SpeechProcessor` and `HTTPVoiceClient`**

Use multipart upload to Whisper `/inference` with `response_format=json`, `temperature=0`, and `language=es`; parse only the transcript field. Post Piper's `{"text": ...}` to `/synthesize`; reject empty, oversized, or malformed audio responses and return sanitized errors.

- [ ] **Step 4: Add failing FFmpeg pipe tests**

Add `TestFFmpegAudioConverterUsesPipesForWAVAndOGGOpus` using a fake executable to verify stdin delivery, output capture, format/codec flags, command cancellation, nonzero exit sanitization, and that no audio file is created.

- [ ] **Step 5: Run conversion tests and verify they fail**

Run: `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 go test ./internal/secretary -run 'TestFFmpegAudioConverter' -count=1`
Expected: FAIL because `AudioConverter` is not implemented.

- [ ] **Step 6: Implement `FFmpegAudioConverter`**

Convert incoming OGG/Opus to mono 16 kHz WAV over pipes; convert Piper WAV output to OGG/Opus for Telegram using `libopus`. Return stable generic errors without command output or input bytes.

- [ ] **Step 7: Run the Task 1 tests and commit**

Run: `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 go test ./internal/secretary -run 'TestHTTPVoiceClient|TestFFmpegAudioConverter' -count=1`
Expected: PASS.

```bash
git add internal/secretary/speech.go internal/secretary/speech_test.go
git commit -m "Add local speech clients and audio conversion"
```

### Task 2: Extend Telegram Polling for Authorized Voice Notes

**Files:**
- Modify: `internal/secretary/telegram.go`
- Modify: `internal/secretary/telegram_test.go`

**Interfaces:**
- Consumes `SpeechProcessor` and `AudioConverter` from Task 1.
- Adds `Voice{FileID string, Duration int, FileSize int64}` and `Message.Voice *Voice` using Telegram JSON field names.
- Adds optional `Speech SpeechProcessor`, `Audio AudioConverter`, `VoiceEnabled bool`, and `VoiceMaxDuration time.Duration` to `Bot`; zero-value fields preserve text-only behavior and a zero duration selects the 120-second default.
- Adds `processVoice(ctx context.Context, message *Message) error` and `sendVoice(ctx context.Context, chat int64, oggOpus []byte) error` on `Bot`.

- [ ] **Step 1: Add failing Telegram parsing and flow tests**

Add tests named `TestVoiceUpdateUnmarshalsTelegramVoice`, `TestUnauthorizedVoiceDoesNotDownloadFile`, `TestVoiceDisabledDoesNotDownloadOrCallInference`, `TestVoiceRejectsOversizedAndOverDurationUpdates`, `TestVoiceTranscribesRepliesTextThenVoice`, `TestVoiceSTTFailureDoesNotCallModel`, `TestVoiceTTSFailureKeepsTextReply`, and `TestVoiceTransportErrorsNeverContainBotToken`. Use an `httptest.Server` to inspect `getFile`, file download, `sendMessage`, and multipart `sendVoice`; assert message text is sent before synthesis/upload. A disabled voice update receives a text notice that voice is disabled and may be sent as text instead.

- [ ] **Step 2: Run Telegram voice tests and verify they fail**

Run: `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 go test ./internal/secretary -run 'TestVoice|TestUnauthorizedVoice' -count=1`
Expected: FAIL because Telegram voice fields and handlers do not exist.

- [ ] **Step 3: Implement the Telegram voice contract and flow**

Decode only the `voice` update field. Keep `Service.Permitted` before `getFile`. Reject `file_size > 20*1024*1024`, duration greater than the configured 120-second default, and downloaded bodies exceeding 20 MiB. Fetch media only through the fixed Telegram file API host; never log its token-bearing URL. Convert OGG/Opus to WAV, call `Transcribe`, pass the transcript once to `Service.Reply`, send the text reply, then synthesize, convert, and send OGG/Opus with Telegram `sendVoice`. Preserve current text and command handling.

- [ ] **Step 4: Run Telegram voice tests and verify they pass**

Run: `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 go test ./internal/secretary -run 'TestVoice|TestUnauthorizedVoice|TestPollingPersistsOffsetRejectsOtherUsersAndDeliversReply' -count=1`
Expected: PASS; forbidden users do not trigger media calls, and TTS failures do not suppress a sent text answer.

- [ ] **Step 5: Commit Task 2**

```bash
git add internal/secretary/telegram.go internal/secretary/telegram_test.go
git commit -m "Handle Telegram voice notes in secretary"
```

### Task 3: Add Voice Metrics, Feature Wiring, and Prometheus Scrape

**Files:**
- Create: `internal/secretary/voice_metrics.go`
- Create: `internal/secretary/voice_metrics_test.go`
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/service_test.go`
- Modify: `internal/secretary/telegram.go`
- Modify: `internal/secretary/telegram_test.go`
- Modify: `cmd/lab-secretary/main.go`
- Modify: `group_vars/all.yml`
- Modify: `playbooks/deploy-secretary.yml`
- Modify: `roles/observability/tasks/main.yml`

**Interfaces:**
- Produces `VoiceMetrics` with `ObservePhase(phase VoicePhase, result VoiceResult, duration time.Duration)` and `ObserveRequest(result VoiceResult, duration time.Duration)`; map unknown enum values to the bounded `other` label.
- Produces metrics `lab_secretary_voice_phase_duration_seconds{phase}`, `lab_secretary_voice_phase_calls_total{phase,result}`, and `lab_secretary_voice_request_duration_seconds{result}`.
- `Service.Handler()` serves its private registry at `GET /metrics`; `Service.VoiceMetrics()` supplies the same registry to `Bot`.

- [ ] **Step 1: Add failing metrics and handler tests**

Add `TestVoiceMetricsExposeDurationsAndBoundedLabels` and `TestSecretaryMetricsEndpointExposesVoiceMetrics`. Assert only the fixed phase values (`stt`, `llm`, `tts`, `telegram_send`, `other`) and result values (`ok`, `error`, `cancelled`, `other`) appear, and assert test marker strings for a transcript, chat ID, user ID, filename, and bot token are absent.

- [ ] **Step 2: Run tests and verify they fail**

Run: `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 go test ./internal/secretary -run 'TestVoiceMetrics|TestSecretaryMetricsEndpoint' -count=1`
Expected: FAIL because the collectors and `/metrics` route do not exist.

- [ ] **Step 3: Implement voice collectors and instrument Telegram phases**

Use the existing `github.com/prometheus/client_golang` dependency and a per-Service registry. Observe `stt`, `llm`, `tts`, and `telegram_send` durations and outcomes in `processVoice`; observe total request duration on every return path. Add no message-derived labels. Serve `/metrics` before the model-key authentication check, as the existing listener is loopback-only and the exposition contains only bounded operational metrics.

- [ ] **Step 4: Wire local endpoints and feature switch**

In `cmd/lab-secretary/main.go`, construct `HTTPVoiceClient` from `LAB_WHISPER_URL` and `LAB_PIPER_URL`, the FFmpeg converter from `/usr/bin/ffmpeg`, and attach metrics to the bot. Add tracked variables for voice enabled, URLs, and the 120-second limit. Keep voice disabled when the flag is false or dependencies are absent; the text API and Telegram path must continue normally.

- [ ] **Step 5: Add Prometheus scrape target and run tests**

In `roles/observability/tasks/main.yml`, add job `lab-secretary` targeting `127.0.0.1:{{ lab_secretary_port }}` whenever `lab_secretary_enabled` is true. Add the `observability` role to `playbooks/deploy-secretary.yml` so the scrape target is reconciled by the same deployment. Run `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 go test ./internal/secretary ./cmd/lab-secretary` and verify all metrics tests pass.

- [ ] **Step 6: Commit Task 3**

```bash
git add internal/secretary/voice_metrics.go internal/secretary/voice_metrics_test.go internal/secretary/service.go internal/secretary/service_test.go internal/secretary/telegram.go internal/secretary/telegram_test.go cmd/lab-secretary/main.go group_vars/all.yml playbooks/deploy-secretary.yml roles/observability/tasks/main.yml docs/superpowers/plans/2026-10-12-telegram-voice-mvp.md
git commit -m "Expose secretary voice performance metrics"
```

### Task 4: Provision Pinned CPU Speech Services

**Files:**
- Create: `roles/lab_speech/defaults/main.yml`
- Create: `roles/lab_speech/tasks/main.yml`
- Create: `roles/lab_speech/handlers/main.yml`
- Modify: `group_vars/all.yml`
- Modify: `playbooks/deploy-secretary.yml`
- Modify: `roles/lab_secretary/tasks/main.yml`

- [ ] **Step 1: Define pinned runtime defaults and role tasks**

Set Whisper source to v1.9.5 commit `d1be6fde11ac6e0407606b4e42fe72d34add8037`, model URL to `https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/ggml-small-q5_1.bin`, and the SHA-256 from Global Constraints. Set Piper to package `piper-tts[http]==1.8.0`, voice ONNX URL `https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/es/es_ES/sharvard/medium/es_ES-sharvard-medium.onnx`, and sibling `es_ES-sharvard-medium.onnx.json`; verify both hashes from Global Constraints. Install FFmpeg, build only the Whisper server target, install Piper in a versioned virtual environment, and place model weights outside the repository under `/srv/ai-lab/lab-speech/models`.

- [ ] **Step 2: Add isolated systemd units and loopback health checks**

Run Whisper on `127.0.0.1:8083` with CPU-only inference, four threads by default, and configurable thread count; omit Whisper's FFmpeg `--convert` mode because the secretary converts incoming OGG/Opus to WAV over pipes. Set `PrivateTmp=yes` and `--tmp-dir /tmp` defensively. Run Piper on `127.0.0.1:8084`. Set separate defaults of `MemoryMax=2G` for Whisper and `MemoryMax=512M` for Piper, plus independent CPU quotas and read-only model paths, `NoNewPrivileges=yes`, and restart handlers. Health-check Whisper's local root page and Piper `/info`.

- [ ] **Step 3: Add switch-based disable and rollback behavior**

Historical plan note: the implementation now separates `lab_secretary_transcription_enabled` (Whisper/STT input) from `lab_secretary_voice_enabled` (Piper/TTS output). Disabling spoken replies must leave transcription enabled when Telegram voice-note input is desired. Do not delete binaries or model files during rollback.

- [ ] **Step 4: Run static deployment checks**

Run: `ansible-playbook --syntax-check playbooks/deploy-secretary.yml` and `git diff --check`. Verify playbook output contains no secret values; do not read or modify `.env`, `group_vars/vault.yml`, or `ssh/`.

- [ ] **Step 5: Commit Task 4**

```bash
git add roles/lab_speech/defaults/main.yml roles/lab_speech/tasks/main.yml roles/lab_speech/handlers/main.yml group_vars/all.yml playbooks/deploy-secretary.yml roles/lab_secretary/tasks/main.yml
git commit -m "Provision local speech services for secretary"
```

### Task 5: Add Smoke Validation, Operator Docs, and Deploy

**Files:**
- Create: `scripts/smoke-secretary-voice.py`
- Modify: `docs/secretary-operation.md`
- Modify: `docs/system-usage.md`
- Create: `docs/validation/2026-10-12-secretary-voice/` evidence files (metadata and timings only)

- [ ] **Step 1: Write the local smoke test**

Add a script that asks Piper to synthesize `Buenos días, esta es una prueba de voz del laboratorio.`, sends the WAV through Whisper `/inference` with `language=es`, checks for the normalized words `buenos`, `prueba`, `voz`, and `laboratorio`, converts a second Piper WAV to OGG/Opus, checks the Ogg container signature, and prints only engine versions, result, and timings. Hold generated media only in process memory and delete any temporary files in `finally` cleanup.

- [ ] **Step 2: Run smoke-script static validation**

Run: `python3 -m py_compile scripts/smoke-secretary-voice.py` and `git diff --check`. Expected: syntax succeeds and the script emits no media content.

- [ ] **Step 3: Update operator documentation**

Document voice-note usage, service ports, metrics names, file/duration limits, privacy behavior, both license notices, CPU tuning variables, and the one-switch rollback that preserves text Telegram behavior.

- [ ] **Step 4: Run repository verification and build the deployed binary**

Run: `./scripts/build-orchestrator-go.sh` (Docker-based Go tests and Linux build) and `ansible-playbook --syntax-check playbooks/deploy-secretary.yml`. Expected: tests and secretary build pass; syntax check passes. Check that generated binaries remain untracked/ignored.

- [ ] **Step 5: Deploy and verify locally on the lab**

Run the approved secretary deployment using the repository's established SSH/Ansible path. Verify both speech services are active and loopback-only, the secretary health check passes, Prometheus reports `up{job="lab-secretary"} == 1`, a synthetic Spanish test passes through Piper → FFmpeg → Whisper → FFmpeg, metrics increment, and no temporary media remain. Then ask the operator to send one short voice note from the authorized Telegram account and verify the text plus playable voice reply. Record versions, hashes, and durations without audio or transcripts.

- [ ] **Step 6: Commit validation evidence and docs**

```bash
git add scripts/smoke-secretary-voice.py docs/secretary-operation.md docs/system-usage.md docs/validation/2026-10-12-secretary-voice
git commit -m "Document and validate Telegram voice MVP"
```

## Execution Method

Use the **Native** method previously selected by the operator: implement the tasks in sequence in this session, then request one fresh whole-branch review before declaring the work complete. Do not dispatch subagents.
