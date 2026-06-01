# StrateCode JetBrains Plugin

This plugin is the IntelliJ Platform shell for the governed multi-agent workflow.

Current scope:

- project context detection
- `.stratecode/project.json` local metadata for bridge and initiative context
- secure orchestrator settings
- orchestrator health and capability status
- bridge matching and registration
- bridge tab with heartbeat staleness and execution gating
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
- pending approvals tab:
  - list pending approvals
  - approve or reject them
- typed initiative summary, review timeline, task backlog, and artifact rendering
- packaged plugin zip generation via Gradle

Useful commands:

```bash
./gradlew test
./gradlew buildPlugin
./gradlew runIde
```

The plugin is intentionally not a Copilot clone. It is a governed client of the orchestrator runtime.
