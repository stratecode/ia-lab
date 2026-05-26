package com.stratecode.lab.jetbrains.actions

import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.actionSystem.ActionUpdateThread
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.application.ApplicationManager
import com.stratecode.lab.jetbrains.client.OrchestratorClient
import com.stratecode.lab.jetbrains.project.ProjectContextResolver
import com.stratecode.lab.jetbrains.settings.PluginSettingsService

class CreateInitiativeFromSelectionAction : AnAction() {
    override fun actionPerformed(event: AnActionEvent) {
        val project = event.project ?: return
        val editor = event.getData(CommonDataKeys.EDITOR) ?: return
        val selection = editor.selectionModel.selectedText?.trim().orEmpty()
        if (selection.isBlank()) {
            notify(project, "No selection", "Select text first. Raw vibes are not enough.", NotificationType.WARNING)
            return
        }
        val settings = ApplicationManager.getApplication().getService(PluginSettingsService::class.java)
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            notify(project, "Missing API key", "Configure StrateCode Agents settings first.", NotificationType.WARNING)
            return
        }
        val context = ProjectContextResolver.resolve(project)
        val fileName = event.getData(CommonDataKeys.VIRTUAL_FILE)?.name ?: context.projectName
        val title = "Selection from $fileName"
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                OrchestratorClient(settings.currentState.baseUrl, apiKey)
                    .createInitiative(title, selection, context.workspaceRoot)
            }.onSuccess {
                notify(project, "Initiative created", "${it.title} (${it.id})", NotificationType.INFORMATION)
            }.onFailure { error ->
                notify(project, "Initiative failed", error.message ?: error.toString(), NotificationType.ERROR)
            }
        }
    }

    override fun update(event: AnActionEvent) {
        event.presentation.isEnabledAndVisible =
            event.project != null &&
                event.getData(CommonDataKeys.EDITOR)?.selectionModel?.hasSelection() == true
    }

    override fun getActionUpdateThread(): ActionUpdateThread = ActionUpdateThread.BGT

    private fun notify(project: com.intellij.openapi.project.Project, title: String, message: String, type: NotificationType) {
        NotificationGroupManager.getInstance()
            .getNotificationGroup("StrateCode Agents")
            .createNotification(title, message, type)
            .notify(project)
    }
}
