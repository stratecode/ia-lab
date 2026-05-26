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

class OrchestratorClient(
    private val baseUrl: String,
    private val apiKey: String,
) {
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        prettyPrint = true
    }
    private val http = HttpClient.newBuilder().build()

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

    fun getInitiativeDetailRaw(initiativeId: String): String =
        getRaw("/initiatives/$initiativeId")

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

    private inline fun <reified T> get(path: String, query: Map<String, String> = emptyMap()): T =
        json.decodeFromString(getRaw(path, query))

    private fun getRaw(path: String, query: Map<String, String> = emptyMap()): String {
        val request = HttpRequest.newBuilder(buildUri(path, query))
            .header("Authorization", "Bearer $apiKey")
            .header("Accept", "application/json")
            .GET()
            .build()
        return send(request)
    }

    private inline fun <reified Req, reified Res> post(path: String, body: Req): Res {
        val request = HttpRequest.newBuilder(buildUri(path))
            .header("Authorization", "Bearer $apiKey")
            .header("Accept", "application/json")
            .header("Content-Type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(json.encodeToString(body)))
            .build()
        return json.decodeFromString(send(request))
    }

    private fun send(request: HttpRequest): String {
        val response = http.send(request, HttpResponse.BodyHandlers.ofString(UTF_8))
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
}
