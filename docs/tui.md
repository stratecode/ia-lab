# TUI Operator Guide

This document describes the current `lab-agent tui` operator surface. It is the pragmatic guide, not a love letter to terminal UI.

## Purpose

The TUI is the primary local operator surface for:

- browsing and advancing initiatives
- launching selective execution from an approved plan
- inspecting task output and artifacts
- resolving approvals
- operating the local bridge
- opening a minimal chat surface

It is not:

- a Markdown editor
- a general IDE
- a substitute for the orchestrator API

## Startup

Linux:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge tui
```

macOS:

```bash
./dist/lab-agent-darwin-arm64 --env-file .env.bridge tui
```

The TUI uses the same connection settings as `lab-agent`:

- `LAB_AGENT_BASE_URL`
- `LAB_AGENT_API_KEY`
- `LAB_AGENT_BRIDGE_ID`
- `LAB_AGENT_WORKSPACE_ROOT`

## Layout

The screen has three stable areas:

1. top header
   - current view
   - workspace root
   - configured bridge id
2. main panel
   - depends on the selected view
3. footer
   - global key reminders
   - last status or last error

## Views

The TUI has intentionally been compressed into a smaller set of views:

1. `Initiatives`
2. `Execution`
3. `Approvals`
4. `Bridge`
5. `Projects`
6. `Chat`
7. `Tasks`

The old split between `Requirements`, `Design`, and `Plan` is now folded into `Initiatives`. That is deliberate. Fewer tabs; more context.

## Global keybindings

These work from normal mode:

- `Ctrl+C`
  - quit
- `Tab`
  - next view
- `Shift+Tab`
  - previous view
- `Ctrl+R`
  - refresh all data
- `Ctrl+F`
  - open filter mode
- `Ctrl+G`
  - open `Chat`
- `Ctrl+P`
  - open `Projects` wizard
- `Ctrl+N`
  - open `Initiatives` wizard

## View details

### `Initiatives`

This is the home view and the center of the workflow.

The left panel shows:

- initiative id
- status
- current phase
- next valid action
- title

The right panel shows the selected initiative:

- status
- current phase
- next valid action
- workspace
- number of linked tasks
- whether backlog is materialized
- aggregated execution state
- initiative goal
- active artifact for the current phase
- artifact version
- diff summary for the active artifact
- latest review
- short recent history for:
  - requirements
  - design
  - plan

#### `Initiatives` actions

- `Up` / `Down`
  - move selection
  - auto-load initiative detail for the new selection
- `Enter`
  - reload selected initiative detail
- `Ctrl+N`
  - open initiative wizard
- `Ctrl+E`
  - generate the next valid phase
  - special case: on `plan_draft`, generate backlog/tasks
- `Ctrl+A`
  - approve the current phase when the initiative is in review
- `Ctrl+X`
  - reject the current phase when the initiative is in review

### `Execution`

This view focuses on initiative-linked tasks and selective launch.

The left panel shows:

- selected initiative
- initiative status
- aggregated execution state
- whether backlog exists
- count of pending manual tasks
- list of initiative tasks with:
  - task id
  - task state
  - execution mode
  - assigned agent
  - launch group / epic
  - short description

The right panel shows the selected task:

- task id
- task state
- execution mode
- assigned agent
- launch group
- workspace path
- policy scope
- allowed modes
- description
- definition of done
- loaded task results
- persisted artifacts

#### `Execution` actions

- `Up` / `Down`
  - move selected initiative task
- `Enter`
  - load selected task detail
- `Ctrl+T`
  - cycle execution mode:
    - `manual`
    - `agent_local`
    - `agent_remote`
- `Ctrl+L`
  - launch the selected task

Notes:

- launch is selective by design
- there is no implicit “launch all”
- policy can block forbidden execution modes

### `Approvals`

The left panel lists pending approvals.

The right panel shows:

- approval id
- task id
- action type
- target resource
- approval status
- timeout

#### `Approvals` actions

- `Up` / `Down`
  - move selection
- `Ctrl+A`
  - approve selected approval
- `Ctrl+X`
  - reject selected approval
- `Enter`
  - load the linked task detail

### `Bridge`

This is the operational panel for the local bridge surface.

It shows:

- configured bridge id
- configured workspace root
- whether the TUI is managing an in-process daemon
- known bridge rows and last heartbeat

#### `Bridge` actions

- `Ctrl+B`
  - register bridge
- `Ctrl+U`
  - send heartbeat now
- `Ctrl+S`
  - smoke-test bridge claim path
- `Enter`
  - start or stop the in-process daemon managed by the TUI

### `Projects`

This is a guided wizard for mini lab projects.

Wizard fields:

- parent directory
- project name
- project type
- runtime / stack
- goal
- test focus
- initialize git
- requires approval

Supported project types today:

- `cli_simple`
- `api_http`
- `web_small`
- `worker_background`
- `debug_regression`
- `toy_repo`

Supported runtime / stack selectors today:

- `python`
- `go`
- `node`
- `static`

#### `Projects` actions

- `Ctrl+P`
  - open wizard
- `Ctrl+N`
  - open wizard as well
- inside wizard:
  - `Up` / `Down` move field
  - `Left` / `Right` change option fields
  - `Space` toggle booleans
  - `Enter` continue or submit
  - `Esc` cancel

This wizard creates a planner root task that fans out to `researcher`, `coder`, and `reviewer`.

### `Chat`

This is intentionally basic.

It supports:

- direct prompt
- response display
- conversion of the prompt into a task

#### `Chat` actions

- type normally
- `Enter`
  - send prompt
- `Ctrl+T`
  - convert current prompt into a task
  - if the prompt box is empty, it reuses the latest chat prompt
- `Esc`
  - leave chat mode

### `Tasks`

This is the raw task inspection view for the operator.

The left panel lists tasks with:

- task id
- state
- execution target
- description

The right panel shows:

- state
- agent
- target
- priority
- archived or not
- update time
- description
- loaded results
- workspace path
- task tree
- artifacts

#### `Tasks` actions

- `Up` / `Down`
  - move selection
- `Enter`
  - load selected task detail
- `Ctrl+X`
  - cancel selected task
- `Ctrl+K`
  - archive selected task
- `Ctrl+U`
  - toggle completed tasks visibility
- `Ctrl+Y`
  - toggle archived tasks visibility

## Form and filter modes

### Filter mode

Opened with `Ctrl+F`.

Behavior:

- if current view is `Approvals`, the filter applies to approvals
- otherwise it applies to tasks

Controls:

- type normally
- `Enter` apply filter
- `Esc` cancel

### Initiative wizard mode

Fields:

- title
- workspace root
- goal

Controls:

- `Up` / `Down`
- `Enter`
- `Esc`

### Project wizard mode

Same structure described in the `Projects` section.

## Initiative lifecycle in the TUI

Canonical happy path:

1. `Ctrl+N`
2. create initiative
3. `Ctrl+E` on requirements
4. `Ctrl+A` or `Ctrl+X`
5. `Ctrl+E` on design
6. `Ctrl+A` or `Ctrl+X`
7. `Ctrl+E` on plan
8. `Ctrl+A` or `Ctrl+X`
9. `Ctrl+E` again on `plan_draft` when task generation is the next step
10. switch to `Execution`
11. use `Ctrl+T` to set mode
12. use `Ctrl+L` to launch selected tasks

## State persistence

The TUI persists lightweight local state at:

```text
~/.config/lab-agent/tui.json
```

It stores:

- last view
- last workspace
- visibility toggles
- recent projects
- recent initiatives
- wizard presets

It does not store:

- API keys
- large task outputs
- server-side artifacts

## Resetting the TUI

If the local operator state is noisy:

```bash
rm -f ~/.config/lab-agent/tui.json
```

If you also want to wipe server-side operational state, use:

- [scripts/reset-orchestrator-state.sh](../scripts/reset-orchestrator-state.sh)

## Operational limits

- the TUI is an operator surface, not a document editor
- execution policy is enforced server-side
- invalid actions are blocked by state, not merely frowned at
- `Chat` is intentionally minimal
- bridge execution still depends on the local daemon and the orchestrator path being healthy

## Troubleshooting

### The TUI feels stale

Cause:

- initiative or task detail was never loaded for the current selection
- server-side state changed while the client still shows cached detail

Fix:

- `Ctrl+R`
- `Enter` on the selected initiative or task

### Chat typing triggers actions

This should no longer happen with the current mapping.

Fix:

- confirm you are running a recent `lab-agent` binary
- rebuild local binaries if needed

### Launch fails from `Execution`

Cause:

- the selected mode violates workspace policy
- the task still requires approval
- the bridge path is unhealthy

Fix:

- inspect the footer error
- inspect the policy block in `Execution`
- inspect `Approvals`
- inspect bridge status
