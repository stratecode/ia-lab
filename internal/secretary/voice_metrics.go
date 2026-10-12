package secretary

import (
	"net/http"
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
	registry        *prometheus.Registry
	phaseDuration   *prometheus.HistogramVec
	phaseCalls      *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
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
	}
	registry.MustRegister(metrics.phaseDuration, metrics.phaseCalls, metrics.requestDuration)
	return metrics
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
