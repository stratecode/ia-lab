# StrateCode JetBrains Plugin

This plugin is the IntelliJ Platform shell for the governed multi-agent workflow.

Current scope:

- project context detection
- `.stratecode/project.json` local metadata for bridge and initiative context
- local-only initiative registry from `.stratecode/project.json`
- secure orchestrator settings
- plan-first `Work` view with:
  - operational header for project, backend, bridge, approval count, and active goal
  - active initiative selector
  - ordered plan steps for the current workspace
  - contextual step detail for overview, output, diff, evidence, and relevant artifacts
- support drawers for:
  - approvals
  - bridge
  - capabilities
  - raw initiative info
  - logs and recent plugin diagnostics
- orchestrator health and capability status
- bridge matching, auto-registration when missing, and heartbeat-based execution gating
- bridge smoke validation from the IDE
- project-scoped capability visibility
- recent initiative listing and detail fetch, scoped to the current `workspace_root`
- server initiatives are only shown if they are already tracked in local workspace metadata
- initiative creation from the tool window or editor selection
- initiative phase actions from the IDE:
  - advance requirements/design drafts
  - approve or reject requirements/design/plan reviews
  - generate the execution-plan task backlog
- task execution controls from the IDE:
  - set `execution_mode` per task step
  - launch the selected task step
- task execution inspection from the IDE:
  - fetch typed task detail and task sources
  - preview diff from `task.results.diff` or patch-like artifacts
  - open the first changed file
  - apply a governed patch with local `git apply`
- reviewer evidence workflow:
  - extract navigable findings from `code_analysis_report`
  - jump directly to `file:line`
  - show raw evidence payloads when coordinates are missing
- approvals drawer:
  - list pending approvals
  - approve or reject them
  - resolve blocking approvals inline from selected task context
- log inspection support:
  - open `idea.log` directly from the plugin
  - inspect the latest plugin-side diagnostics without leaving the panel
- local reset action:
  - clears `.stratecode/project.json`
  - forgets tracked initiatives for the workspace
  - forces a clean local state
- typed initiative summary, review timeline, task backlog, and artifact rendering
- packaged plugin zip generation via Gradle

Useful commands:

```bash
./gradlew test
./gradlew buildPlugin
./gradlew runIde
```

The plugin is intentionally not a Copilot clone. It is a governed client of the orchestrator runtime with a plan-first workflow.
