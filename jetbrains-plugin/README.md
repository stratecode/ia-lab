# StrateCode JetBrains Plugin

This plugin is the IntelliJ Platform shell for the governed multi-agent workflow.

Current scope:

- project context detection
- `.stratecode/project.json` local metadata for bridge and initiative context
- secure orchestrator settings
- task-first `Work` view with:
  - operational header for project, backend, bridge, and approval count
  - active initiative selector
  - filtered backlog for the current workspace
  - contextual task detail for summary, diff, evidence, and task artifacts
- support drawers for:
  - approvals
  - bridge
  - capabilities
  - initiative info
- orchestrator health and capability status
- bridge matching, registration, and heartbeat-based execution gating
- bridge smoke validation from the IDE
- project-scoped capability visibility
- recent initiative listing and detail fetch, scoped to the current `workspace_root`
- initiative creation from the tool window or editor selection
- initiative phase actions from the IDE:
  - advance requirements/design drafts
  - approve or reject requirements/design/plan reviews
  - generate the execution-plan task backlog
- task execution controls from the IDE:
  - set `execution_mode` per task
  - launch one or many selected tasks
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
- typed initiative summary, review timeline, task backlog, and artifact rendering
- packaged plugin zip generation via Gradle

Useful commands:

```bash
./gradlew test
./gradlew buildPlugin
./gradlew runIde
```

The plugin is intentionally not a Copilot clone. It is a governed client of the orchestrator runtime.
