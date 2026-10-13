package secretary

import (
	"strings"
	"sync"
	"time"
)

const DefaultProgressDelay = 30 * time.Second

type progressTracker struct {
	mu     sync.Mutex
	delay  time.Duration
	stage  string
	timer  *time.Timer
	sent   bool
	closed bool
	emit   func(string)
	prefix string
}

type interactionProgressTracker struct {
	mu     sync.Mutex
	delay  time.Duration
	stage  string
	timer  *time.Timer
	sent   bool
	closed bool
	emit   InteractionObserver
}

func newInteractionProgressTracker(delay time.Duration, emit InteractionObserver) *interactionProgressTracker {
	if delay <= 0 {
		delay = DefaultProgressDelay
	}
	p := &interactionProgressTracker{delay: delay, stage: "interpretando la consulta", emit: emit}
	if emit != nil {
		p.timer = time.AfterFunc(delay, p.sendDelayed)
	}
	return p
}

func (p *interactionProgressTracker) Observe(event InteractionEvent) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	switch event.Kind {
	case EventIntentRecognized:
		p.stage = "preparando la respuesta"
	case EventStageChanged:
		if strings.TrimSpace(event.Stage) != "" {
			p.stage = strings.TrimSpace(event.Stage)
		}
	case EventLookupStarted:
		p.stage = "consultando " + boundedLabel(event.Source)
	case EventActionRecognized:
		p.stage = "comprobando la acción solicitada"
	case EventActionStarted:
		p.stage = "ejecutando " + boundedLabel(event.Action)
	case EventWorkDelayed:
		p.stage = strings.TrimSpace(event.Stage)
	}
	if event.Kind == EventCompleted || event.Kind == EventBlocked {
		p.closed = true
		if p.timer != nil {
			p.timer.Stop()
		}
	}
	emit := p.emit
	p.mu.Unlock()
	if event.Kind != EventStageChanged && event.Kind != EventWorkDelayed && emit != nil {
		emit(event)
	}
}

func (p *interactionProgressTracker) sendDelayed() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed || p.sent || p.emit == nil || p.stage == "" {
		p.mu.Unlock()
		return
	}
	p.sent = true
	event := InteractionEvent{Kind: EventWorkDelayed, Stage: p.stage}
	emit := p.emit
	p.mu.Unlock()
	emit(event)
}

func (p *interactionProgressTracker) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
}

func boundedLabel(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) > 80 {
		return string([]rune(value)[:80])
	}
	return value
}

func newProgressTracker(delay time.Duration, emit func(string)) *progressTracker {
	if delay <= 0 {
		delay = DefaultProgressDelay
	}
	return &progressTracker{delay: delay, emit: emit, prefix: "Sigo con ello: "}
}

func (p *progressTracker) SetPrefix(prefix string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.prefix = strings.TrimSpace(prefix) + " "
	p.mu.Unlock()
}

func (p *progressTracker) SetStage(stage string) {
	if p == nil {
		return
	}
	stage = strings.TrimSpace(strings.TrimSuffix(stage, "."))
	if stage == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.stage = stage
	if p.timer == nil && !p.sent {
		p.timer = time.AfterFunc(p.delay, p.sendDelayed)
	}
}

func (p *progressTracker) sendDelayed() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.sent || p.emit == nil || p.stage == "" {
		return
	}
	p.sent = true
	p.emit(p.prefix + p.stage + ".")
}

func (p *progressTracker) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
}
