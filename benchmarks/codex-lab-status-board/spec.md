# Status Board Mini Web App Spec

Build a small static service status dashboard.

## Functional Requirements

Use the services in `src/data.js`.

Implement these exported functions in `src/dashboard.js`:

- `formatLatency(ms)`: returns `"n/a"` for `null`/`undefined`, `"<1 ms"` for `0`, and `"<number> ms"` for positive numbers.
- `summarizeServices(services)`: returns `{ total, up, degraded, down, averageLatencyMs }`, where average latency ignores missing latency and rounds to the nearest integer.
- `filterServices(services, filters)`: supports `query`, `status`, and `category`; empty values mean no filter.
- `sortServices(services)`: sorts by severity first (`down`, `degraded`, `up`), then by latency descending, then by name ascending.
- `statusLabel(status)`: returns `Operational`, `Degraded`, or `Down`.

Implement the UI in `index.html`, `src/main.js`, and `src/styles.css`:

- Render one card per service.
- Show summary numbers: total, up, degraded, down, average latency.
- Add a search input with id `service-search`.
- Add status filter buttons with `data-filter-status`.
- Add category filter select with id `category-filter`.
- Search and filters must update the visible cards without reload.
- Include an empty state for no matches.
- Use semantic status classes: `status-up`, `status-degraded`, `status-down`.
- Use accessible labels for search/filter controls.
- Use responsive CSS with a media query.

## Visual Direction

Use a distinctive but practical dashboard style:

- Background: deep graphite or ink base, not plain white.
- Accent: amber/cyan/green operational colors.
- Typography: confident dashboard type scale using system-safe fonts.
- Layout: compact header, summary strip, controls row, responsive service grid.
- Cards: clear status rail or dot, readable latency, category, and endpoint.
- No generic purple SaaS gradient.

## Acceptance Commands

```bash
npm test
npm run quality
```

Both must pass.
