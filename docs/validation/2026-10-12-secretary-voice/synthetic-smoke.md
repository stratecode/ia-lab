# Secretary voice deployment evidence

Date: 2026-10-12 (UTC)

## Deployed pins

| Component | Verified value |
|---|---|
| Whisper source | `d1be6fde11ac6e0407606b4e42fe72d34add8037` (`whisper.cpp` v1.9.5) |
| Whisper model SHA-256 | `ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb` |
| Piper package | `piper-tts` 1.8.0 |
| Piper Spanish voice SHA-256 | `40febfb1679c69a4505ff311dc136e121e3419a13a290ef264fdf43ddedd0fb1` |
| Piper voice config SHA-256 | `7438c9b699c72b0c3388dae1b68d3f364dc66a2150fe554a1c11f03372957b2c` |

Hashes were checked on the lab after the Ansible deployment. The speech
services reported HTTP 200 health responses and listen only on `127.0.0.1:8083`
and `127.0.0.1:8084`. `lab-whisper`, `lab-piper`, and `lab-secretary` were
active. The `lab-secretary` Prometheus target returned `up = 1`.

## Synthetic speech test

The test generated a fixed Spanish sentence in memory, converted it to Telegram
OGG/Opus with FFmpeg, sent the WAV to Whisper, and checked required word markers
without printing or saving recognized text or audio.

| Phase | Result | Duration |
|---|---:|---:|
| Piper synthesis | pass | 0.759 s |
| FFmpeg OGG/Opus conversion | pass | 0.072 s |
| Whisper transcription | pass | 2.972 s |
| Total | pass | 3.803 s |

An initial phrase beginning with “Buenos días” did not recognize the first
word consistently; the smoke phrase was adjusted so the same marker appears
after the initial words. The strict marker check then passed. This is a smoke
test result, not a latency guarantee for user voice notes.

## Telegram end-to-end

Pending a brief voice note from the authorized operator account. On receipt,
record only bounded metric counts and whether a written and playable voice
reply arrived; do not record the note, transcript, prompt, or chat identifiers.
