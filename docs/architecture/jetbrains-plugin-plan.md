# JetBrains Plugin Plan

This document is the canonical execution spec for the JetBrains plugin that turns the IDE into the governed operating console for the platform.

It is an implementation document, not a mood board. If an adjacent note disagrees on plugin scope, v1 behavior, or architecture, this document wins.

## Current implementation status

Implemented now:

- canonical spec and repo layout
- IntelliJ Platform plugin scaffold in Kotlin under `/jetbrains-plugin`
- secure settings for orchestrator `base_url`, `api_key`, `bridge_name`, and policy mode
- project context resolution for `workspace_root`, branch, and normalized `repository_url`
- local project metadata persisted in `.stratecode/project.json`
- `Agents` tool window with:
  - backend readiness
  - bridge resolution
  - bridge tab with staleness and executable/non-executable state
  - bridge smoke validation
  - capability visibility
  - project-scoped capability visibility
  - initiative creation
  - recent initiative listing scoped to the current workspace
  - typed initiative detail, review timeline, and task backlog rendering
  - phase actions:
    - advance requirements/design drafts
    - approve/reject requirements/design/plan
    - generate plan backlog
  - task execution controls:
    - set execution mode per task
    - launch selected tasks
  - pending approvals visibility and resolution
- editor popup action: `Create Initiative from Selection`
- Gradle wrapper, tests, and distributable plugin zip build

Not implemented yet:

- diff preview and governed patch application
- navigation from reviewer evidence to file/line
- bridge task/daemon introspection beyond heartbeat-level health

## Product intent

The plugin exists to make the platform usable from a real IDE without turning the IDE into an ungoverned chat toy.

V1 is intentionally narrow:

- IntelliJ Platform plugin
- validated first on IntelliJ IDEA
- local project bound to one bridge-backed workspace
- initiative-first workflow
- governed editor primitives
- no fake autonomy, no RPA, no shell cosplay through popup menus

The plugin is not:

- a replacement IDE
- a JetBrains-native clone of Codex
- an unrestricted agent cockpit
- a second orchestrator control plane

Its job is to expose the current orchestrator runtime, bridge, initiatives, approvals, artifacts, diffs, and project-scoped capabilities inside the IDE in a way that is useful for engineering work.

## Delivery shape

Repository location:

- `/jetbrains-plugin`

Primary runtime dependencies:

- orchestrator HTTP API
- project-scoped capability broker
- local bridge registration and status
- initiative lifecycle endpoints

Validated V1 operating flow:

1. open a real Git project in JetBrains
2. discover `workspace_root` and `repository_url`
3. validate or register the local bridge
4. inspect effective project capabilities
5. create an initiative from prompt or editor selection
6. review requirements, design, and plan
7. generate and launch selected tasks
8. approve gated coder work when required
9. inspect researcher/coder/reviewer outputs
10. open findings, diffs, and artifacts
11. apply a governed patch from the IDE

## Architecture

### Runtime model

The plugin is a rich client of the current Go control plane.

It consumes:

- `GET /ready`
- `GET /capabilities`
- `GET /capabilities/definitions`
- `GET /projects/capabilities`
- `GET /bridges`
- `POST /bridges/register`
- `GET /initiatives`
- `POST /initiatives`
- `GET /initiatives/{id}`
- `GET /initiatives/{id}/artifacts`
- `POST /initiatives/{id}/advance`
- `POST /initiatives/{id}/approve/{phase}`
- `POST /initiatives/{id}/reject/{phase}`
- `POST /initiatives/{id}/tasks/generate`
- `GET /initiatives/{id}/tasks`
- `POST /initiatives/{id}/tasks/launch`
- `GET /approvals`
- `POST /approvals/{id}/approve`
- `POST /approvals/{id}/reject`

It uses editor primitives only:

- read current file
- read current selection
- open file by path
- jump to file position
- show diff
- apply patch with confirmation

The plugin does not invent a second task model or a private capability system.

### Local bridge policy

V1 binds one JetBrains project window to one logical bridge workspace.

Rules:

- if the current project path and bridge `workspace_root` do not match, execution is blocked
- if `repository_url` is missing, the plugin enters degraded mode
- if the project-scoped policy denies the required capability, the plugin shows a governed denial and stops

### Security model

- API key stored in JetBrains secure storage
- no secrets persisted in plaintext under the repo
- no write action outside the currently opened project root
- no hidden autonomous edits
- all real execution still goes through orchestrator policy plus bridge allowlist

## Phases

### Phase 0

Goal: freeze the contract before adding implementation sprawl.

Deliverables:

- this document
- repo location decision
- V1 boundaries
- API contract list
- acceptance criteria

### Phase 1

Goal: plugin shell with project discovery and backend connectivity.

Required:

- IntelliJ Platform project in Kotlin
- tool window
- secure settings for `base_url`, `api_key`, `bridge_name`, `project_policy_mode`
- project context resolution:
  - `workspace_root`
  - branch
  - normalized `repository_url`
- health readout from `/ready`
- effective capability readout from `/capabilities` and `/projects/capabilities`
- bridge list and match status from `/bridges`

### Phase 2

Goal: initiative creation and read/write lifecycle governance.

Required:

- create initiative
- list recent initiatives
- view initiative detail
- advance and review phases
- generate tasks
- view task backlog
- launch selected tasks
- resolve pending approvals
- inspect initiative artifacts

### Phase 3

Goal: bridge-aware local execution governance.

Required:

- register bridge
- show bridge status and staleness
- enforce bridge/project consistency before launch

Status:

- implemented

### Phase 4

Goal: editor primitives and governed patch workflow.

Required:

- open files from reviewer or artifact references
- show diff
- apply patch with confirmation
- navigate from evidence to code

### Phase 5

Goal: v1 closure as an operational multi-agent console.

Required:

- initiative governance end to end from the IDE
- local launch
- approvals
- artifact and diff inspection
- patch application

### Phase 6

Goal: hardening.

Required:

- reconnect behavior
- clean error states
- structured logging
- request timeout separation
- robust degraded-mode UX

## UI model

Primary surface:

- one tool window: `Agents`

V1 tabs:

- `Status`
- `Initiatives`
- `Approvals`
- `Bridge`
- `Artifacts` (embedded in initiative detail)

Phase 4+ additions:

- `Artifacts`
- `Diffs`

Context actions:

- `Create Initiative from Selection`
- `Open Reviewer Evidence`
- `Apply Orchestrator Patch`

## Acceptance

V1 is closed when a user can:

1. open a repo in JetBrains
2. connect to the orchestrator
3. validate bridge and capabilities
4. create and govern an initiative
5. launch approved local work
6. inspect outputs and evidence
7. apply a patch from the IDE

without depending on Telegram, TUI, or curl for the normal path.

## Deferred beyond V1

- full autonomy loop from inside the IDE
- multi-workspace orchestration from one project window
- CI/log readers and broader reviewer data feeds
- generalized MCP marketplace UX
- IDE-side task replay or offline mutation queues
