export const services = [
  {
    name: "Gateway",
    url: "https://codex.stratecode.com/health",
    category: "Edge",
    status: "up",
    latencyMs: 42
  },
  {
    name: "llama.cpp",
    url: "http://127.0.0.1:8080/health",
    category: "Inference",
    status: "degraded",
    latencyMs: 890
  },
  {
    name: "Prometheus",
    url: "http://127.0.0.1:9090/-/ready",
    category: "Observability",
    status: "up",
    latencyMs: 78
  },
  {
    name: "Grafana",
    url: "https://monitor.stratecode.com",
    category: "Observability",
    status: "down",
    latencyMs: null
  },
  {
    name: "WireGuard",
    url: "10.66.66.1",
    category: "Network",
    status: "up",
    latencyMs: 12
  },
  {
    name: "Codex Host",
    url: "ssh://stratecode.local",
    category: "Runtime",
    status: "degraded",
    latencyMs: 260
  }
];
