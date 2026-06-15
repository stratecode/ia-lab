You are generating complete replacement files for a small static web app benchmark.

Return ONLY file blocks in this exact format:

===FILE:index.html===
<full file content>
===END_FILE===
===FILE:src/dashboard.js===
<full file content>
===END_FILE===
===FILE:src/main.js===
<full file content>
===END_FILE===
===FILE:src/styles.css===
<full file content>
===END_FILE===

Do not include Markdown fences.
Do not include explanations.
Do not claim tests were run.
Do not add dependencies.

Task:
- Implement the Status Board mini web app described in SPEC.md.
- Make `npm test` pass.
- Make `npm run quality` pass.
- Use only vanilla HTML, CSS, and ESM JavaScript.
- Keep the public API in `src/dashboard.js` exactly as specified.
