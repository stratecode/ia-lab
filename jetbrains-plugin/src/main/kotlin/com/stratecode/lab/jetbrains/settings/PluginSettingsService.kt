package com.stratecode.lab.jetbrains.settings

import com.intellij.credentialStore.CredentialAttributes
import com.intellij.credentialStore.Credentials
import com.intellij.ide.passwordSafe.PasswordSafe
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage

data class PluginSettingsState(
    var baseUrl: String = "https://lab.stratecode.com/orchestrator",
    var bridgeName: String = "jetbrains-bridge",
    var projectPolicyMode: String = "project_scoped",
)

@Service(Service.Level.APP)
@State(name = "StrateCodePluginSettings", storages = [Storage("stratecode-agents.xml")])
class PluginSettingsService : PersistentStateComponent<PluginSettingsState> {
    private var persistedState = PluginSettingsState()

    val currentState: PluginSettingsState
        get() = persistedState

    override fun getState(): PluginSettingsState = persistedState

    override fun loadState(state: PluginSettingsState) {
        this.persistedState = state
    }

    fun getApiKey(): String {
        val credentials = PasswordSafe.instance.get(attributes())
        return credentials?.getPasswordAsString().orEmpty()
    }

    fun setApiKey(apiKey: String) {
        PasswordSafe.instance.set(attributes(), Credentials("stratecode", apiKey))
    }

    private fun attributes(): CredentialAttributes =
        CredentialAttributes("StrateCode Agents API Key")
}
