# Isolated LLM benchmark

## Objective

Compare the four models already used in the lab and the nine proposed candidates with the same ten fixed capability cases, one model process loaded at a time, measuring inference performance and scoring answer quality.

## Steps

1. Pin each candidate to an upstream GGUF revision, filename, byte count, and SHA-256; download missing files to the lab and verify every artifact.
2. Transfer the existing benchmark runner and versioned cases to a temporary host directory; stop the secretary and all inference services so only the benchmark model consumes resources.
3. Start each model sequentially on loopback with the same CPU-only llama.cpp parameters; run the ten cases, collect TTFT, total duration, generated tokens, throughput, and peak RSS; stop and verify process exit before starting the next model.
4. Restore the lab services and verify their health. Score each model against the fixed rubric and publish a reproducible results table with limitations.

## Safety and validation

- No production API keys or connector queries are used; benchmark prompts are synthetic and calls remain on loopback.
- No changes are made to model defaults or deployment configuration. Existing services are restarted after the isolated run.
- Verify download size/hash, exactly ten results per model, one benchmark inference process at a time, final service health, report consistency, and `git diff --check`.
