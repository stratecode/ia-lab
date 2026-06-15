You are generating a patch for a small static web app benchmark.

Return ONLY a unified git diff patch that can be applied with `git apply`.
Do not wrap the patch in Markdown fences.
Do not include explanations.
Do not claim tests were run.

Task:
- Implement the Status Board mini web app described in SPEC.md.
- Make `npm test` pass.
- Make `npm run quality` pass.
- Do not add dependencies.
- Use only vanilla HTML, CSS, and ESM JavaScript.
- Keep paths exactly as shown.

Patch requirements:
- Modify only these files:
  - `index.html`
  - `src/dashboard.js`
  - `src/main.js`
  - `src/styles.css`
- The patch must be relative to the repository root shown in the file context below.
