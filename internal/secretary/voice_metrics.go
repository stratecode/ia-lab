package secretary

import (
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type VoicePhase string

const (
	VoicePhaseSTT          VoicePhase = "stt"
	VoicePhaseLLM          VoicePhase = "llm"
	VoicePhaseTTS          VoicePhase = "tts"
	VoicePhaseTelegramSend VoicePhase = "telegram_send"
)

type VoiceResult string

const (
	VoiceResultOK        VoiceResult = "ok"
	VoiceResultError     VoiceResult = "error"
	VoiceResultCancelled VoiceResult = "cancelled"
)

type VoiceMetrics struct {
	registry         *prometheus.Registry
	phaseDuration    *prometheus.HistogramVec
	phaseCalls       *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	interactions     *prometheus.CounterVec
	interactionTime  *prometheus.HistogramVec
	memoryRetrieval  *prometheus.CounterVec
	memoryHits       prometheus.Histogram
	traceWriteError  prometheus.Counter
	maintenanceError prometheus.Counter
	guardrail        *prometheus.CounterVec
	responseWords    *prometheus.HistogramVec
	answerAdjust     *prometheus.CounterVec
	modelTokens      *prometheus.CounterVec
	proactive        *prometheus.CounterVec
}

func NewVoiceMetrics() *VoiceMetrics {
	registry := prometheus.NewRegistry()
	buckets := []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 300}
	metrics := &VoiceMetrics{
		registry: registry,
		phaseDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lab_secretary_voice_phase_duration_seconds",
			Help:    "Duration of bounded phases in a secretary voice request.",
			Buckets: buckets,
		}, []string{"phase"}),
		phaseCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lab_secretary_voice_phase_calls_total",
			Help: "Calls completed for bounded phases in a secretary voice request.",
		}, []string{"phase", "result"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lab_secretary_voice_request_duration_seconds",
			Help:    "Total duration of a secretary voice request.",
			Buckets: buckets,
		}, []string{"result"}),
		interactions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lab_secretary_interactions_total",
			Help: "Completed secretary interactions by bounded intent, capability, and outcome.",
		}, []string{"intent", "capability", "outcome"}),
		interactionTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lab_secretary_interaction_duration_seconds",
			Help:    "Duration of a secretary interaction.",
			Buckets: buckets,
		}, []string{"intent", "capability", "outcome"}),
		memoryRetrieval: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lab_secretary_memory_retrieval_total",
			Help: "Memory retrieval attempts by bounded outcome.",
		}, []string{"outcome"}),
		memoryHits: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "lab_secretary_memory_retrieval_hits",
			Help:    "Number of approved memory items returned per retrieval.",
			Buckets: []float64{0, 1, 2, 3, 5, 10, 20},
		}),
		traceWriteError: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lab_secretary_trace_write_errors_total",
			Help: "Learning trace writes that failed without affecting the response.",
		}),
		maintenanceError: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "lab_secretary_learning_maintenance_errors_total",
			Help: "Private learning retention cleanup failures.",
		}),
		guardrail: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lab_secretary_guardrail_redactions_total",
			Help: "Sensitive values redacted before text left the service, by closed category. Never carries the redacted content.",
		}, []string{"category"}),
		responseWords: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lab_secretary_response_words",
			Help:    "Words in final secretary answers by bounded intent. Used to keep answers short.",
			Buckets: []float64{5, 10, 20, 30, 45, 60, 90, 130, 200, 400},
		}, []string{"intent"}),
		answerAdjust: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lab_secretary_answer_adjustments_total",
			Help: "Model answers that exceeded the length contract and were regenerated or trimmed.",
		}, []string{"action"}),
		modelTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lab_secretary_model_tokens_total",
			Help: "Prompt and completion tokens reported by the model backend for secretary requests.",
		}, []string{"kind"}),
	}
	metrics.proactive = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "lab_secretary_proactive_total",
		Help: "Scheduled-skill and proactive-message events by closed event name; never message content.",
	}, []string{"event"})
	registry.MustRegister(metrics.phaseDuration, metrics.phaseCalls, metrics.requestDuration, metrics.interactions, metrics.interactionTime, metrics.memoryRetrieval, metrics.memoryHits, metrics.traceWriteError, metrics.maintenanceError, metrics.guardrail, metrics.responseWords, metrics.answerAdjust, metrics.modelTokens, metrics.proactive)
	for _, category := range guardrailCategories {
		metrics.guardrail.WithLabelValues(category)
	}
	for _, action := range []string{"regenerated", "trimmed"} {
		metrics.answerAdjust.WithLabelValues(action)
	}
	for _, kind := range []string{"prompt", "completion"} {
		metrics.modelTokens.WithLabelValues(kind)
	}
	return metrics
}

// ObserveAnswerAdjustment counts a length-contract correction.
func (m *VoiceMetrics) ObserveAnswerAdjustment(action string) {
	if m == nil || (action != "regenerated" && action != "trimmed") {
		return
	}
	m.answerAdjust.WithLabelValues(action).Inc()
}

// ObserveModelTokens records backend-reported token usage, ignoring absent values.
func (m *VoiceMetrics) ObserveModelTokens(prompt, completion int) {
	if m == nil {
		return
	}
	if prompt > 0 {
		m.modelTokens.WithLabelValues("prompt").Add(float64(prompt))
	}
	if completion > 0 {
		m.modelTokens.WithLabelValues("completion").Add(float64(completion))
	}
}

// ObserveResponseWords records the length of a final answer without its text.
func (m *VoiceMetrics) ObserveResponseWords(intent string, words int) {
	if m == nil || words < 0 {
		return
	}
	m.responseWords.WithLabelValues(boundedInteractionIntent(intent)).Observe(float64(words))
}

// ObserveGuardrail records redaction counts using only the closed category set.
func (m *VoiceMetrics) ObserveGuardrail(report GuardrailReport) {
	if m == nil {
		return
	}
	for _, category := range guardrailCategories {
		if n := report[category]; n > 0 {
			m.guardrail.WithLabelValues(category).Add(float64(n))
		}
	}
}

func (m *VoiceMetrics) ObserveInteraction(intent, capability, outcome string, duration time.Duration) {
	if m == nil {
		return
	}
	labels := []string{boundedInteractionIntent(intent), boundedCapability(capability), boundedInteractionOutcome(outcome)}
	seconds := duration.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	m.interactions.WithLabelValues(labels...).Inc()
	m.interactionTime.WithLabelValues(labels...).Observe(seconds)
}

func (m *VoiceMetrics) ObserveMemoryRetrieval(outcome string, hits int) {
	if m == nil {
		return
	}
	if hits < 0 {
		hits = 0
	}
	m.memoryRetrieval.WithLabelValues(boundedMemoryOutcome(outcome)).Inc()
	m.memoryHits.Observe(float64(hits))
}

func (m *VoiceMetrics) ObserveTraceWriteError() {
	if m != nil {
		m.traceWriteError.Inc()
	}
}

func (m *VoiceMetrics) ObserveMaintenanceError() {
	if m != nil {
		m.maintenanceError.Inc()
	}
}

func boundedInteractionIntent(value string) string {
	switch value {
	case "general", "tool", "memory", "clarify", "ambiguous", "agreement", "action":
		return value
	default:
		return "other"
	}
}

func boundedCapability(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "granola":
		return "granola"
	case "jira":
		return "jira"
	case "memory":
		return "memory"
	case "none", "":
		return "none"
	default:
		return "other"
	}
}

func boundedInteractionOutcome(value string) string {
	switch value {
	case "success", "empty", "error", "blocked":
		return value
	default:
		return "other"
	}
}

func boundedMemoryOutcome(value string) string {
	switch value {
	case "hit", "miss", "error", "skipped":
		return value
	default:
		return "other"
	}
}

func (m *VoiceMetrics) ObservePhase(phase VoicePhase, result VoiceResult, duration time.Duration) {
	if m == nil {
		return
	}
	phaseLabel := boundedVoicePhase(phase)
	resultLabel := boundedVoiceResult(result)
	seconds := duration.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	m.phaseDuration.WithLabelValues(phaseLabel).Observe(seconds)
	m.phaseCalls.WithLabelValues(phaseLabel, resultLabel).Inc()
}

func (m *VoiceMetrics) ObserveRequest(result VoiceResult, duration time.Duration) {
	if m == nil {
		return
	}
	seconds := duration.Seconds()
	if seconds < 0 {
		seconds = 0
	}
	m.requestDuration.WithLabelValues(boundedVoiceResult(result)).Observe(seconds)
}

func (m *VoiceMetrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func boundedVoicePhase(phase VoicePhase) string {
	switch phase {
	case VoicePhaseSTT, VoicePhaseLLM, VoicePhaseTTS, VoicePhaseTelegramSend:
		return string(phase)
	default:
		return "other"
	}
}

func boundedVoiceResult(result VoiceResult) string {
	switch result {
	case VoiceResultOK, VoiceResultError, VoiceResultCancelled:
		return string(result)
	default:
		return "other"
	}
}

// proactiveEvents is the closed set of proactive event names; anything else is
// dropped so a label can never carry task, summary or message text.
var proactiveEvents = map[string]bool{
	"proactive_claimed": true, "proactive_queued": true, "proactive_suppressed": true, "proactive_skill_error": true,
	"proactive_delivered": true, "proactive_send_failed": true, "proactive_send_uncertain": true, "proactive_expired": true,
	"proactive_deferred_quiet_hours": true, "proactive_deferred_daily_limit": true, "proactive_refused_destination": true,
	"proactive_state_error": true, "proactive_shown_openwebui": true,
}

// ObserveProactive counts one proactive event by its closed name.
func (m *VoiceMetrics) ObserveProactive(event string) {
	if m == nil || !proactiveEvents[event] {
		return
	}
	m.proactive.WithLabelValues(event).Inc()
}
