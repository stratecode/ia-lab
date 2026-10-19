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
	}
	registry.MustRegister(metrics.phaseDuration, metrics.phaseCalls, metrics.requestDuration, metrics.interactions, metrics.interactionTime, metrics.memoryRetrieval, metrics.memoryHits, metrics.traceWriteError, metrics.maintenanceError)
	return metrics
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
