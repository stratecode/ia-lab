import { readFile } from "node:fs/promises";

const [html, main, css] = await Promise.all([
  readFile(new URL("../index.html", import.meta.url), "utf8"),
  readFile(new URL("../src/main.js", import.meta.url), "utf8"),
  readFile(new URL("../src/styles.css", import.meta.url), "utf8")
]);

const checks = [
  ["html_has_search", /id=["']service-search["']/.test(html), 10],
  ["html_has_status_filters", /data-filter-status=["']up["']/.test(html) && /data-filter-status=["']degraded["']/.test(html) && /data-filter-status=["']down["']/.test(html), 10],
  ["html_has_summary_and_empty_state", /summary/i.test(html) && /id=["']empty-state["']/.test(html), 10],
  ["html_accessible_controls", /aria-label=/.test(html) && /<label/i.test(html), 10],
  ["js_renders_from_data", /services/.test(main) && /map|forEach/.test(main) && /innerHTML|createElement/.test(main), 15],
  ["js_wires_interactions", /addEventListener\(["']input["']/.test(main) && /addEventListener\(["']click["']/.test(main) && /addEventListener\(["']change["']/.test(main), 15],
  ["css_design_tokens", /:root/.test(css) && /--color-up/.test(css) && /--color-down/.test(css), 10],
  ["css_responsive_grid", /grid-template-columns/.test(css) && /@media/.test(css), 10],
  ["css_not_plain_default", /background/.test(css) && /linear-gradient|radial-gradient|#0|#1|rgb/.test(css), 10]
];

const details = checks.map(([name, pass, points]) => ({ name, pass, points: pass ? points : 0, max: points }));
const score = details.reduce((total, item) => total + item.points, 0);
const maxScore = details.reduce((total, item) => total + item.max, 0);
const result = {
  score,
  maxScore,
  pass: score >= 80,
  details
};

console.log(JSON.stringify(result, null, 2));

if (!result.pass) {
  process.exitCode = 1;
}
