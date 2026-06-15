import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const html = await readFile(new URL("../index.html", import.meta.url), "utf8");
const main = await readFile(new URL("../src/main.js", import.meta.url), "utf8");
const css = await readFile(new URL("../src/styles.css", import.meta.url), "utf8");

test("HTML exposes required dashboard controls and regions", () => {
  assert.match(html, /id=["']service-search["']/);
  assert.match(html, /id=["']category-filter["']/);
  assert.match(html, /data-filter-status=["']up["']/);
  assert.match(html, /data-filter-status=["']degraded["']/);
  assert.match(html, /data-filter-status=["']down["']/);
  assert.match(html, /id=["']service-grid["']/);
  assert.match(html, /id=["']empty-state["']/);
  assert.match(html, /aria-label=/);
});

test("main.js renders cards and wires search/filter interactions", () => {
  assert.match(main, /addEventListener\(["']input["']/);
  assert.match(main, /addEventListener\(["']click["']/);
  assert.match(main, /addEventListener\(["']change["']/);
  assert.match(main, /service-grid/);
  assert.match(main, /status-/);
  assert.match(main, /filterServices/);
  assert.match(main, /summarizeServices/);
});

test("CSS defines a responsive, status-aware dashboard system", () => {
  assert.match(css, /:root/);
  assert.match(css, /--color-up/);
  assert.match(css, /--color-degraded/);
  assert.match(css, /--color-down/);
  assert.match(css, /\.status-up/);
  assert.match(css, /\.status-degraded/);
  assert.match(css, /\.status-down/);
  assert.match(css, /grid-template-columns/);
  assert.match(css, /@media/);
});
