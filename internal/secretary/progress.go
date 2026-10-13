package secretary

import (
	"strings"
	"sync"
	"time"
)

const defaultProgressDelay = 5 * time.Second

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

func newProgressTracker(delay time.Duration, emit func(string)) *progressTracker {
	if delay <= 0 {
		delay = defaultProgressDelay
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
