# StrateCode JetBrains Plugin

This plugin is the IntelliJ Platform shell for the governed multi-agent workflow.

Current scope:

- project context detection
- `.stratecode/project.json` local metadata for bridge and initiative context
- secure orchestrator settings
- orchestrator health and capability status
- bridge matching and registration
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
