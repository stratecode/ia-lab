You are implementing the benchmark task in this workspace.

Read `SPEC.md` first. Implement the static Status Board mini web app.

Hard constraints:
- Do not install dependencies.
- Use only vanilla HTML, CSS, and ESM JavaScript.
- Keep the public API in `src/dashboard.js` exactly as specified.
- Make `npm test` pass.
- Make `npm run quality` pass.
- Keep the app usable by opening `index.html` directly or serving the folder statically.

Recommended order:
1. Run `npm test` to see the failing baseline.
2. Implement `src/dashboard.js`.
3. Implement `index.html`, `src/main.js`, and `src/styles.css`.
4. Run `npm test`.
5. Run `npm run quality`.
6. Fix until both pass.

Final response must include:
- files changed
- commands run
- whether tests pass
- any known limitation
