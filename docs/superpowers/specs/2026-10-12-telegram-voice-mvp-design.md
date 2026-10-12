# Telegram voice MVP design

**Status:** Draft for operator review  
**Date:** 2026-10-12

## Goal

Enable the existing `lab-secretary` Telegram bot to accept Spanish voice notes and return the normal written answer plus a spoken answer. Keep speech processing on the lab host and make the speech engine reusable by future WhatsApp and Slack adapters.

## User experience and scope

- This is a voice-note round trip, not a live call or partial-audio streaming session.
- On an authorized Telegram chat, a voice note is downloaded, transcribed in Spanish, sent through the existing secretary conversation, and answered with text followed by a Telegram voice message.
- Text messages and existing commands keep their current behavior.
- If transcription fails, the bot reports that it could not understand the recording and asks for another. If synthesis or audio delivery fails, the written answer remains available.
- The MVP processes Telegram only. WhatsApp and Slack can reuse the speech service and normalized speech flow, but require independent channel adapters, authentication, and media handling.

## Architecture

`lab-secretary` remains the conversation owner and Telegram adapter. Add an internal speech interface with two operations: transcribe an audio payload and synthesize speech from the assistant's reply. The Telegram adapter calls this interface around the existing `Service.Reply` path.

Run the speech engines as separate host services, reachable only over loopback. This respects the current 128 MB memory limit on `lab-secretary` and allows each engine to load and retain its model independently:

- **STT:** `whisper.cpp`, multilingual Spanish `small` model in a quantized build. Pin source revision, model URL, and SHA-256 in deployment configuration. Validate CPU latency on the lab before widening the model or thread count.
- **TTS:** maintained `OHF-Voice/piper1-gpl`, using an `es_ES` medium voice as the initial candidate. Validate pronunciation and intelligibility with Spanish responses before treating the voice as final. Pin package version and model URL/checksum.
- **Audio conversion:** install FFmpeg through the tracked deployment role. Convert incoming Telegram OGG/Opus as required by the STT endpoint, and Piper WAV output to OGG/Opus for Telegram `sendVoice`.

The shared speech interface is channel-neutral. Channel adapters own media download, sender authorization, platform-specific formats, and delivery. No public speech endpoint is added.

## Privacy and limits

- Retain the existing Telegram user allowlist; reject unauthorized updates before downloading media or calling speech services.
- Enforce Telegram's cloud download limit (20 MB) and a configurable audio-duration ceiling. Reject oversized input with a short user-facing message.
- Keep intermediate media in private temporary storage and delete it after success or failure. Do not add raw audio or transcripts to logs, metrics, or persistent conversation history beyond the existing text conversation behavior.
- Metrics use bounded labels such as channel, phase, and result. Never label with user IDs, chat IDs, transcripts, prompts, filenames, or Telegram file URLs.
- Speech services listen on `127.0.0.1`; systemd units use dedicated users, restrictive filesystem settings, and independent resource limits.
- Do not commit model weights or executable build outputs. Commit reproducible versions, source/model URLs, checksums, license notices, playbook changes, and validation evidence.

## Latency and observability

Continue collecting LLM request metrics through the existing telemetry proxy. Add secretary voice metrics for transcription duration, synthesis duration, total voice-request duration, and phase errors. Send the text answer as soon as the LLM response is ready, before waiting for TTS, so speech generation does not delay access to the written result. Record no user content.

The first live validation records a small set of representative Spanish voice notes and reports STT, model, TTS, and end-to-end durations separately. It also checks response intelligibility and correct Telegram voice rendering. CPU thread and duration limits remain configurable so the test can tune responsiveness without changing the speech API.

## Deployment and rollback

Extend the secretary deployment path with tracked Ansible tasks and systemd units for FFmpeg, Whisper, Piper, their model files, and local health checks. Pin dependencies and verify downloaded model hashes before activation. Keep the existing text path operational when speech services are disabled or unhealthy. A single configuration switch must disable voice handling while preserving text Telegram behavior.

Rollback disables voice in configuration and stops the two speech services; it does not remove the existing text bot or its data.

## Validation criteria

1. Go tests cover Telegram voice update parsing, authorization before media retrieval, file-size and duration limits, STT-to-secretary-to-TTS sequencing, cleanup on both success and error, and text-only fallback when TTS fails.
2. Tests verify that logs and metrics contain no audio, transcript, prompt, user ID, or bot token.
3. The repository's Go test harness and deployment validation pass.
4. On the lab, health checks pass for both local speech services and an authorized Telegram voice note produces a readable text answer and a playable voice reply. An unauthorized user cannot trigger media download or inference.
5. Validation evidence records versions, checksums, sample duration, phase timings, and observed quality without retaining the sample audio or transcript in committed artifacts.

## Open implementation checks

- Confirm the selected `es_ES` Piper voice card and license before downloading the model; the engine's GPL license does not establish the voice model's license.
- Confirm the exact Whisper multilingual quantized artifact and its SHA-256 from the upstream model index when preparing the deployment plan.
- Measure whether `small` quantized Whisper meets the desired voice-note latency on the actual CPU; adjust thread limits or model size only from those measurements.

## Primary sources

- [whisper.cpp](https://github.com/ggml-org/whisper.cpp): CPU-capable multilingual inference, available model families, quantization, and memory guidance.
- [OHF-Voice Piper HTTP API](https://github.com/OHF-Voice/piper1-gpl/blob/main/docs/API_HTTP.md): local synthesis endpoint and runtime operation.
- [Piper maintained package metadata](https://github.com/OHF-Voice/piper1-gpl/blob/main/setup.py): package version and GPL-3.0-or-later license.
- [Piper voice catalogue](https://github.com/rhasspy/piper/blob/master/VOICES.md): Spanish (Spain) voices; check each voice's model card independently.
- [Telegram Bot API](https://core.telegram.org/bots/api): `getFile` download constraints and `sendVoice` supported output.
- [WhatsApp Cloud API media](https://www.postman.com/meta/whatsapp-business-platform/folder/13382743-ecb27be5-4d27-4763-bbee-6a8002c04bf3): distinct media upload/download flow for a future adapter.
- [Slack file upload flow](https://api.slack.com/messaging/files/remote): current external upload and share sequence for a future adapter.
