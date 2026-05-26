package com.stratecode.lab.jetbrains.settings

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.options.Configurable
import com.intellij.openapi.ui.Messages
import com.intellij.ui.components.JBPasswordField
import com.intellij.ui.components.JBTextField
import java.awt.GridBagConstraints
import java.awt.GridBagLayout
import javax.swing.JComboBox
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JPanel

class PluginSettingsConfigurable : Configurable {
    private val baseUrlField = JBTextField()
    private val apiKeyField = JBPasswordField()
    private val bridgeNameField = JBTextField()
    private val policyModeCombo = JComboBox(arrayOf("project_scoped", "discover_filter"))
    private var panel: JPanel? = null

    override fun getDisplayName(): String = "StrateCode Agents"

    override fun createComponent(): JComponent {
        val settings = settings()
        baseUrlField.text = settings.currentState.baseUrl
        apiKeyField.text = settings.getApiKey()
        bridgeNameField.text = settings.currentState.bridgeName
        policyModeCombo.selectedItem = settings.currentState.projectPolicyMode

        panel = JPanel(GridBagLayout()).apply {
            val gbc = GridBagConstraints().apply {
                fill = GridBagConstraints.HORIZONTAL
                anchor = GridBagConstraints.NORTHWEST
                weightx = 1.0
                insets = java.awt.Insets(6, 6, 6, 6)
            }
            addRow(this, gbc, 0, "Base URL", baseUrlField)
            addRow(this, gbc, 1, "API Key", apiKeyField)
            addRow(this, gbc, 2, "Bridge Name", bridgeNameField)
            addRow(this, gbc, 3, "Policy Mode", policyModeCombo)
        }
        return panel!!
    }

    override fun isModified(): Boolean {
        val settings = settings()
        return baseUrlField.text != settings.currentState.baseUrl ||
            String(apiKeyField.password) != settings.getApiKey() ||
            bridgeNameField.text != settings.currentState.bridgeName ||
            policyModeCombo.selectedItem?.toString() != settings.currentState.projectPolicyMode
    }

    override fun apply() {
        val settings = settings()
        val baseUrl = baseUrlField.text.trim()
        if (baseUrl.isBlank()) {
            throw IllegalArgumentException("Base URL is required")
        }
        settings.currentState.baseUrl = baseUrl.removeSuffix("/")
        settings.currentState.bridgeName = bridgeNameField.text.trim().ifBlank { "jetbrains-bridge" }
        settings.currentState.projectPolicyMode = policyModeCombo.selectedItem?.toString().orEmpty().ifBlank { "project_scoped" }
        settings.setApiKey(String(apiKeyField.password).trim())
        Messages.showInfoMessage("Plugin settings updated.", "StrateCode Agents")
    }

    override fun reset() {
        val settings = settings()
        baseUrlField.text = settings.currentState.baseUrl
        apiKeyField.text = settings.getApiKey()
        bridgeNameField.text = settings.currentState.bridgeName
        policyModeCombo.selectedItem = settings.currentState.projectPolicyMode
    }

    private fun addRow(panel: JPanel, gbc: GridBagConstraints, row: Int, label: String, field: JComponent) {
        val left = gbc.clone() as GridBagConstraints
        left.gridx = 0
        left.gridy = row
        left.weightx = 0.0
        panel.add(JLabel(label), left)

        val right = gbc.clone() as GridBagConstraints
        right.gridx = 1
        right.gridy = row
        right.weightx = 1.0
        panel.add(field, right)
    }

    private fun settings(): PluginSettingsService =
        ApplicationManager.getApplication().getService(PluginSettingsService::class.java)
}
