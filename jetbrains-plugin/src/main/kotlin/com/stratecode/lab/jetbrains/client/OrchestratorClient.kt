package com.stratecode.lab.jetbrains.client

import com.stratecode.lab.jetbrains.project.ProjectContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import java.net.InetAddress
import java.net.URI
import java.net.URLEncoder
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.charset.StandardCharsets
import java.time.Duration
import java.net.http.HttpTimeoutException
import java.net.ConnectException
import kotlin.text.Charsets.UTF_8

@Serializable
data class ReadyResponse(val ready: Boolean = false, val checks: Map<String, Boolean> = emptyMap())

@Serializable
data class CapabilityCandidate(
    val name: String = "",
    val kind: String = "",
    @SerialName("capability_tags") val capabilityTags: List<String> = emptyList(),
    val score: Double = 0.0,
)

@Serializable
data class CapabilitiesResponse(
    @SerialName("repository_url") val repositoryUrl: String? = null,
    val capabilities: List<CapabilityCandidate> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class EffectiveProjectCapabilitiesResponse(
    @SerialName("repository_url") val repositoryUrl: String = "",
    val mode: String = "",
    val version: String = "",
    val capabilities: List<CapabilityCandidate> = emptyList(),
)

@Serializable
data class LocalBridgeResponse(
    val id: String = "",
    val name: String = "",
    val hostname: String = "",
    @SerialName("workspace_root") val workspaceRoot: String = "",
    val status: String = "",
    @SerialName("last_heartbeat") val lastHeartbeat: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

@Serializable
data class LocalBridgeListResponse(
    val items: List<LocalBridgeResponse> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class LocalBridgeRegisterRequest(
    @SerialName("bridge_id") val bridgeId: String,
    val name: String,
    val hostname: String,
    @SerialName("workspace_root") val workspaceRoot: String,
    val capabilities: Map<String, JsonElement> = emptyMap(),
    @SerialName("api_key_name") val apiKeyName: String? = null,
)

@Serializable
data class InitiativeSummary(
    val id: String,
    val title: String,
    val status: String,
    @SerialName("current_phase") val currentPhase: String,
    @SerialName("workspace_root") val workspaceRoot: String,
    @SerialName("created_at") val createdAt: String,
) {
    override fun toString(): String = "$title [$status / $currentPhase]"
}

@Serializable
data class InitiativeListResponse(
    val items: List<InitiativeSummary> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class InitiativeCreateRequest(
    val title: String,
    @SerialName("workspace_root") val workspaceRoot: String,
    val goal: String,
    @SerialName("created_by") val createdBy: String,
    @SerialName("execution_mode") val executionMode: String = "selective",
)

@Serializable
data class InitiativeActionRequest(
    val feedback: String = "",
    val operator: String? = null,
)

@Serializable
data class InitiativeRecord(
    val id: String,
    val title: String,
    @SerialName("workspace_root") val workspaceRoot: String,
    val goal: String,
    val status: String,
    @SerialName("current_phase") val currentPhase: String,
    @SerialName("created_by") val createdBy: String,
    @SerialName("execution_mode") val executionMode: String,
    @SerialName("created_at") val createdAt: String,
    @SerialName("updated_at") val updatedAt: String,
)

@Serializable
data class InitiativePhaseReviewRecord(
    val id: String,
    @SerialName("initiative_id") val initiativeId: String,
    val phase: String,
    val decision: String,
    val feedback: String? = null,
    @SerialName("generated_by") val generatedBy: String? = null,
    @SerialName("created_at") val createdAt: String,
)

@Serializable
data class InitiativeHistoryEntryRecord(
    val version: Int,
    @SerialName("diff_summary") val diffSummary: String? = null,
    @SerialName("artifact_type") val artifactType: String? = null,
    @SerialName("created_at") val createdAt: String,
)

@Serializable
data class InitiativePhaseHistoryRecord(
    val phase: String,
    @SerialName("active_version") val activeVersion: Int,
    val items: List<InitiativeHistoryEntryRecord>? = emptyList(),
)

@Serializable
data class InitiativeExecutionSummaryRecord(
    @SerialName("backlog_materialized") val backlogMaterialized: Boolean = false,
    @SerialName("aggregated_status") val aggregatedStatus: String = "",
    @SerialName("task_count") val taskCount: Int = 0,
    @SerialName("pending_manual") val pendingManual: Int = 0,
)

@Serializable
data class InitiativeExecutionPolicyRecord(
    @SerialName("workspace_root") val workspaceRoot: String = "",
    val scope: String = "",
    @SerialName("allowed_modes") val allowedModes: List<String> = emptyList(),
    @SerialName("approval_required_modes") val approvalRequiredModes: List<String> = emptyList(),
)

@Serializable
data class InitiativeTaskRecord(
    val id: String,
    val state: String,
    val description: String,
    @SerialName("assigned_agent") val assignedAgent: String? = null,
    @SerialName("planned_agent") val plannedAgent: String? = null,
    val priority: String,
    @SerialName("execution_target") val executionTarget: String,
)

@Serializable
data class InitiativeTaskLinkRecord(
    @SerialName("initiative_id") val initiativeId: String,
    @SerialName("task_id") val taskId: String,
    @SerialName("phase_origin") val phaseOrigin: String,
    val epic: String? = null,
    @SerialName("launch_group") val launchGroup: String? = null,
    @SerialName("execution_mode") val executionMode: String,
    @SerialName("launch_order") val launchOrder: Int,
    val task: InitiativeTaskRecord,
)

@Serializable
data class InitiativeTaskListResponseRecord(
    val items: List<InitiativeTaskLinkRecord> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class InitiativeArtifactRecord(
    val id: String,
    @SerialName("artifact_type") val artifactType: String,
    val title: String? = null,
    val uri: String? = null,
    @SerialName("media_type") val mediaType: String? = null,
    @SerialName("content_text") val contentText: String? = null,
    val metadata: Map<String, JsonElement> = emptyMap(),
    @SerialName("created_at") val createdAt: String,
)

@Serializable
data class InitiativeArtifactListResponseRecord(
    val items: List<InitiativeArtifactRecord> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class TaskDetailRecord(
    val id: String,
    val state: String,
    val description: String,
    @SerialName("workspace_path") val workspacePath: String? = null,
    val results: JsonElement? = null,
    @SerialName("error_message") val errorMessage: String? = null,
    @SerialName("updated_at") val updatedAt: String,
)

@Serializable
data class TaskSourcesResponseRecord(
    val items: List<InitiativeArtifactRecord> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class InitiativeDetailResponseRecord(
    val initiative: InitiativeRecord,
    val reviews: List<InitiativePhaseReviewRecord> = emptyList(),
    val histories: List<InitiativePhaseHistoryRecord> = emptyList(),
    @SerialName("execution_summary") val executionSummary: InitiativeExecutionSummaryRecord = InitiativeExecutionSummaryRecord(),
    @SerialName("execution_policy") val executionPolicy: InitiativeExecutionPolicyRecord = InitiativeExecutionPolicyRecord(),
)

@Serializable
data class InitiativeGenerateTasksResponse(
    val initiative: InitiativeRecord,
    val tasks: List<InitiativeTaskLinkRecord> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class InitiativeLaunchTasksRequest(
    @SerialName("task_ids") val taskIds: List<String> = emptyList(),
    val groups: List<String> = emptyList(),
    @SerialName("mode_overrides") val modeOverrides: Map<String, String> = emptyMap(),
)

@Serializable
data class InitiativeTaskModeRequest(
    @SerialName("execution_mode") val executionMode: String,
)

@Serializable
data class InitiativeLaunchTasksResponse(
    val initiative: InitiativeRecord,
    val tasks: List<InitiativeTaskRecord> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class ApprovalRecord(
    val id: String,
    @SerialName("task_id") val taskId: String,
    @SerialName("action_type") val actionType: String,
    @SerialName("target_resource") val targetResource: String,
    val status: String,
    val operator: String? = null,
    @SerialName("timeout_seconds") val timeoutSeconds: Int = 0,
    @SerialName("escalation_level") val escalationLevel: Int = 0,
    @SerialName("requested_at") val requestedAt: String,
    @SerialName("resolved_at") val resolvedAt: String? = null,
    @SerialName("timeout_at") val timeoutAt: String,
)

@Serializable
data class ApprovalListResponseRecord(
    val items: List<ApprovalRecord> = emptyList(),
    val total: Int = 0,
)

@Serializable
data class ApprovalResolveRequest(
    val operator: String,
)

class OrchestratorClient(
    private val baseUrl: String,
    private val apiKey: String,
    private val trace: ((String) -> Unit)? = null,
) {
    companion object {
        private val connectTimeout: Duration = Duration.ofSeconds(5)
        private val requestTimeout: Duration = Duration.ofSeconds(20)
    }

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        prettyPrint = true
    }
    private val http = HttpClient.newBuilder()
        .connectTimeout(connectTimeout)
        .build()

    fun checkReady(): ReadyResponse = get("/ready")

    fun listCapabilities(repositoryUrl: String?, intent: String, agentType: String, preferredTags: List<String>): CapabilitiesResponse {
        val params = linkedMapOf(
            "intent" to intent,
            "agent_type" to agentType,
        )
        if (!repositoryUrl.isNullOrBlank()) {
            params["repository_url"] = repositoryUrl
        }
        if (preferredTags.isNotEmpty()) {
            params["preferred_tags"] = preferredTags.joinToString(",")
        }
        return get("/capabilities", params)
    }

    fun getProjectCapabilities(repositoryUrl: String, intent: String, agentType: String): EffectiveProjectCapabilitiesResponse =
        get(
            "/projects/capabilities",
            mapOf(
                "repository_url" to repositoryUrl,
                "intent" to intent,
                "agent_type" to agentType,
            ),
        )

    fun listBridges(): LocalBridgeListResponse = get("/bridges")

    fun registerBridge(context: ProjectContext, bridgeName: String): LocalBridgeResponse {
        val payload = LocalBridgeRegisterRequest(
            bridgeId = context.stableBridgeId,
            name = bridgeName,
            hostname = InetAddress.getLocalHost().hostName,
            workspaceRoot = context.workspaceRoot,
        )
        return post("/bridges/register", payload)
    }

    fun listInitiatives(limit: Int = 20): InitiativeListResponse =
        get("/initiatives", mapOf("limit" to limit.toString()))

    fun listInitiatives(workspaceRoot: String, limit: Int = 20): InitiativeListResponse =
        get(
            "/initiatives",
            mapOf(
                "limit" to limit.toString(),
                "workspace_root" to workspaceRoot,
            ),
        )

    fun getInitiativeDetailRaw(initiativeId: String): String =
        getRaw("/initiatives/$initiativeId")

    fun getInitiativeDetail(initiativeId: String): InitiativeDetailResponseRecord =
        get("/initiatives/$initiativeId")

    fun listInitiativeTasks(initiativeId: String): InitiativeTaskListResponseRecord =
        get("/initiatives/$initiativeId/tasks")

    fun listInitiativeArtifacts(initiativeId: String): InitiativeArtifactListResponseRecord =
        get("/initiatives/$initiativeId/artifacts")

    fun getTask(taskId: String): TaskDetailRecord =
        get("/tasks/$taskId")

    fun getTaskSources(taskId: String): TaskSourcesResponseRecord =
        get("/tasks/$taskId/sources")

    fun createInitiative(title: String, goal: String, workspaceRoot: String): InitiativeSummary {
        val created = post<InitiativeCreateRequest, JsonElement>(
            "/initiatives",
            InitiativeCreateRequest(
                title = title,
                goal = goal,
                workspaceRoot = workspaceRoot,
                createdBy = "jetbrains-plugin",
            ),
        )
        return json.decodeFromString(created.toString())
    }

    fun advanceInitiative(initiativeId: String, feedback: String = ""): InitiativeRecord =
        post("/initiatives/$initiativeId/advance", InitiativeActionRequest(feedback = feedback))

    fun approveInitiativePhase(initiativeId: String, phase: String, operator: String, feedback: String = ""): InitiativeRecord =
        post(
            "/initiatives/$initiativeId/approve/$phase",
            InitiativeActionRequest(feedback = feedback, operator = operator),
        )

    fun rejectInitiativePhase(initiativeId: String, phase: String, operator: String, feedback: String = ""): InitiativeRecord =
        post(
            "/initiatives/$initiativeId/reject/$phase",
            InitiativeActionRequest(feedback = feedback, operator = operator),
        )

    fun generateInitiativeTasks(initiativeId: String, feedback: String = ""): InitiativeGenerateTasksResponse =
        post("/initiatives/$initiativeId/tasks/generate", InitiativeActionRequest(feedback = feedback))

    fun launchInitiativeTasks(
        initiativeId: String,
        taskIds: List<String>,
        modeOverrides: Map<String, String> = emptyMap(),
    ): InitiativeLaunchTasksResponse =
        post(
            "/initiatives/$initiativeId/tasks/launch",
            InitiativeLaunchTasksRequest(taskIds = taskIds, modeOverrides = modeOverrides),
        )

    fun updateInitiativeTaskMode(initiativeId: String, taskId: String, executionMode: String): InitiativeTaskLinkRecord =
        post(
            "/initiatives/$initiativeId/tasks/$taskId/mode",
            InitiativeTaskModeRequest(executionMode),
        )

    fun listApprovals(statusFilter: String = "pending", limit: Int = 50): ApprovalListResponseRecord =
        get(
            "/approvals",
            mapOf(
                "status_filter" to statusFilter,
                "limit" to limit.toString(),
            ),
        )

    fun approveApproval(approvalId: String, operator: String): ApprovalRecord =
        post("/approvals/$approvalId/approve", ApprovalResolveRequest(operator))

    fun rejectApproval(approvalId: String, operator: String): ApprovalRecord =
        post("/approvals/$approvalId/reject", ApprovalResolveRequest(operator))

    private inline fun <reified T> get(path: String, query: Map<String, String> = emptyMap()): T =
        json.decodeFromString(getRaw(path, query))

    private fun getRaw(path: String, query: Map<String, String> = emptyMap()): String {
        val request = HttpRequest.newBuilder(buildUri(path, query))
            .timeout(requestTimeout)
            .header("Authorization", "Bearer $apiKey")
            .header("Accept", "application/json")
            .GET()
            .build()
        return send(request)
    }

    private inline fun <reified Req, reified Res> post(path: String, body: Req): Res {
        val request = HttpRequest.newBuilder(buildUri(path))
            .timeout(requestTimeout)
            .header("Authorization", "Bearer $apiKey")
            .header("Accept", "application/json")
            .header("Content-Type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(json.encodeToString(body)))
            .build()
        return json.decodeFromString(send(request))
    }

    private fun send(request: HttpRequest): String {
        val startedAt = System.nanoTime()
        trace?.invoke("HTTP ${request.method()} ${request.uri()}")
        val response = try {
            http.send(request, HttpResponse.BodyHandlers.ofString(UTF_8))
        } catch (error: HttpTimeoutException) {
            val elapsedMs = elapsedMs(startedAt)
            trace?.invoke("HTTP timeout after ${elapsedMs}ms ${request.method()} ${request.uri()}")
            error("Request timed out after ${requestTimeout.seconds}s: ${request.uri()}")
        } catch (error: ConnectException) {
            val elapsedMs = elapsedMs(startedAt)
            trace?.invoke("HTTP connect failure after ${elapsedMs}ms ${request.method()} ${request.uri()} -> ${error.message ?: "unreachable host"}")
            error("Connection failed to $baseUrl: ${error.message ?: "unreachable host"}")
        } catch (error: Exception) {
            val elapsedMs = elapsedMs(startedAt)
            trace?.invoke("HTTP request failure after ${elapsedMs}ms ${request.method()} ${request.uri()} -> ${error.message ?: error::class.java.simpleName}")
            error("Request failed for ${request.uri()}: ${error.message ?: error::class.java.simpleName}")
        }
        val elapsedMs = elapsedMs(startedAt)
        trace?.invoke("HTTP ${response.statusCode()} in ${elapsedMs}ms ${request.method()} ${request.uri()}")
        if (response.statusCode() !in 200..299) {
            error("HTTP ${response.statusCode()}: ${response.body()}")
        }
        return response.body()
    }

    private fun buildUri(path: String, query: Map<String, String> = emptyMap()): URI {
        val normalizedBase = baseUrl.removeSuffix("/")
        val queryString = query.entries
            .filter { it.value.isNotBlank() }
            .joinToString("&") {
                "${encode(it.key)}=${encode(it.value)}"
            }
        val suffix = if (queryString.isBlank()) "" else "?$queryString"
        return URI.create("$normalizedBase$path$suffix")
    }

    private fun encode(value: String): String = URLEncoder.encode(value, StandardCharsets.UTF_8)

    private fun elapsedMs(startedAt: Long): Long = (System.nanoTime() - startedAt) / 1_000_000
}
