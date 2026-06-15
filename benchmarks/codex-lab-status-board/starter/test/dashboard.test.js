import assert from "node:assert/strict";
import test from "node:test";
import {
  filterServices,
  formatLatency,
  sortServices,
  statusLabel,
  summarizeServices
} from "../src/dashboard.js";
import { services } from "../src/data.js";

test("formatLatency handles missing, zero, and positive values", () => {
  assert.equal(formatLatency(null), "n/a");
  assert.equal(formatLatency(undefined), "n/a");
  assert.equal(formatLatency(0), "<1 ms");
  assert.equal(formatLatency(42), "42 ms");
});

test("summarizeServices counts statuses and rounds average latency", () => {
  assert.deepEqual(summarizeServices(services), {
    total: 6,
    up: 3,
    degraded: 2,
    down: 1,
    averageLatencyMs: 256
  });
});

test("filterServices supports query, status, and category filters", () => {
  assert.deepEqual(
    filterServices(services, { query: "graf", status: "", category: "" }).map((service) => service.name),
    ["Grafana"]
  );
  assert.deepEqual(
    filterServices(services, { query: "", status: "degraded", category: "" }).map((service) => service.name),
    ["llama.cpp", "Codex Host"]
  );
  assert.deepEqual(
    filterServices(services, { query: "", status: "", category: "Observability" }).map((service) => service.name),
    ["Prometheus", "Grafana"]
  );
  assert.deepEqual(
    filterServices(services, { query: "code", status: "degraded", category: "Runtime" }).map((service) => service.name),
    ["Codex Host"]
  );
});

test("sortServices orders by severity, latency, then name", () => {
  assert.deepEqual(sortServices(services).map((service) => service.name), [
    "Grafana",
    "llama.cpp",
    "Codex Host",
    "Prometheus",
    "Gateway",
    "WireGuard"
  ]);
});

test("statusLabel returns user-facing status labels", () => {
  assert.equal(statusLabel("up"), "Operational");
  assert.equal(statusLabel("degraded"), "Degraded");
  assert.equal(statusLabel("down"), "Down");
  assert.equal(statusLabel("unknown"), "Unknown");
});
