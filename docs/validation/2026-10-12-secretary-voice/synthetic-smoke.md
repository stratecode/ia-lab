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
| Piper synthesis | pass | 0.909 s |
| FFmpeg OGG/Opus conversion | pass | 0.078 s |
| Whisper transcription | pass | 2.956 s |
| Total | pass | 3.944 s |

An initial phrase beginning with “Buenos días” did not recognize the first
word consistently; the smoke phrase was adjusted so the same marker appears
after the initial words. The strict marker check then passed. This is a smoke
test result, not a latency guarantee for user voice notes.

After redirect protection was added, the updated secretary binary was
redeployed and the synthetic test passed again with the timings above.

## Telegram end-to-end

Verified on 2026-10-13 after the operator sent a private voice note. Since the
secretary's last restart, its in-memory counters show two successful voice
requests. STT, LLM, and TTS each completed twice; Telegram send completed four
times, matching one text and one voice send per request. Prometheus returned the
same counts and `up{job="lab-secretary"} = 1`. Telegram accepted the voice sends;
this confirms delivery API success, not that the operator played the audio.

| Measurement | Count | Aggregate | Mean |
|---|---:|---:|---:|
| STT | 2 | 6.125 s | 3.063 s |
| LLM | 2 | 4.145 s | 2.072 s |
| TTS | 2 | 1.807 s | 0.904 s |
| Telegram sends (text + voice) | 4 | 1.184 s | 0.296 s |
| Voice request | 2 | 13.598 s | 6.799 s |

Only bounded counts and durations are recorded. The note, transcription, prompt,
and chat identifiers were not inspected or added to evidence.
