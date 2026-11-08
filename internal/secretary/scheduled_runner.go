package secretary

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ScheduledTaskExecutor runs one occurrence of a task. The production
// implementation is the built-in skill runner; tests inject fakes.
type ScheduledTaskExecutor interface {
	Execute(ctx context.Context, task ScheduledTask, scheduledFor time.Time) (ScheduledSkillResult, error)
}

// SkillExecutor adapts a ScheduledSkillRunner to ScheduledTaskExecutor.
type SkillExecutor struct{ Runner *ScheduledSkillRunner }

func (e SkillExecutor) Execute(ctx context.Context, task ScheduledTask, scheduledFor time.Time) (ScheduledSkillResult, error) {
	return e.Runner.Run(ctx, task, scheduledFor)
}

// ProactiveSendOutcome is what a channel adapter knows after a send.
type ProactiveSendOutcome int

const (
	// SendDelivered: the channel accepted the message.
	SendDelivered ProactiveSendOutcome = iota
	// SendFailed: the channel rejected the message before accepting it. Safe to retry.
	SendFailed
	// SendUncertain: the request may have been accepted (timeout, dropped
	// connection). Never retried automatically.
	SendUncertain
)

// ProactiveSender delivers one message to a private operator chat.
type ProactiveSender interface {
	Send(ctx context.Context, chatID int64, text string) ProactiveSendOutcome
}

const (
	scheduledPollInterval = time.Minute
	scheduledClaimBatch   = 5
)

// ProactiveScheduler claims due tasks once a minute, runs them one at a time,
// queues each result in the outbox and delivers it within policy. It runs on the
// secretary's own process context and exists only when the feature flag is on.
type ProactiveScheduler struct {
	Tasks    *ScheduledTaskStore
	Outbox   *ProactiveOutbox
	Executor ScheduledTaskExecutor
	Sender   ProactiveSender
	Now      func() time.Time
	// Interval is the polling period; zero means one minute.
	Interval time.Duration
	// Event receives closed, body-free event names for logs.
	Event   func(string)
	Metrics *VoiceMetrics
}

func (p *ProactiveScheduler) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

func (p *ProactiveScheduler) event(name string) {
	if p.Event != nil {
		p.Event(name)
	}
	p.Metrics.ObserveProactive(name)
}

// Run polls until ctx ends. It returns immediately on a misconfigured scheduler.
func (p *ProactiveScheduler) Run(ctx context.Context) {
	if p.Tasks == nil || p.Outbox == nil || p.Executor == nil {
		return
	}
	interval := p.Interval
	if interval <= 0 {
		interval = scheduledPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	p.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.RunOnce(ctx)
		}
	}
}

// RunOnce is one polling pass: expire stale items, execute due occurrences
// sequentially (at most one scheduled inference at a time), then deliver.
func (p *ProactiveScheduler) RunOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	p.expire()
	claims, err := p.Tasks.ClaimDue(p.now(), scheduledClaimBatch)
	if err != nil {
		p.event("proactive_state_error")
		return
	}
	for _, claim := range claims {
		if ctx.Err() != nil {
			return // left running on purpose: the next process resumes it
		}
		p.event("proactive_claimed")
		p.execute(ctx, claim)
	}
	if ctx.Err() == nil {
		p.deliver(ctx)
	}
}

func (p *ProactiveScheduler) expire() {
	expired, err := p.Outbox.Expire()
	if err != nil {
		p.event("proactive_state_error")
		return
	}
	for _, n := range expired {
		p.finishRun(n.RunID, RunSkipped, "expired")
		p.event("proactive_expired")
	}
}

func (p *ProactiveScheduler) finishRun(runID string, status ScheduledRunStatus, code string) {
	if err := p.Tasks.FinishRun(runID, status, code); err != nil {
		p.event("proactive_state_error")
	}
}

func (p *ProactiveScheduler) execute(ctx context.Context, claim ClaimedRun) {
	task, run := claim.Task, claim.Run
	// A previous process may have queued this occurrence and died before closing the run.
	if existing, ok, err := p.Outbox.ForOccurrence(task.ID, run.ScheduledFor); err == nil && ok {
		if existing.Status == ProactiveDelivered || existing.Status == ProactiveUncertain || existing.Status == ProactiveExpired {
			p.finishRun(run.ID, mapNotificationStatus(existing.Status), "")
		} else if err := p.Tasks.QueueRun(run.ID); err != nil {
			p.event("proactive_state_error")
		}
		return
	}
	result, err := p.Executor.Execute(ctx, task, run.ScheduledFor)
	if err != nil {
		if ctx.Err() != nil {
			return // shutdown mid-run: keep the claim for resume
		}
		code := "skill_error"
		if errors.Is(err, ErrUnknownScheduledSkill) {
			code = "unknown_skill"
		}
		p.finishRun(run.ID, RunFailed, code)
		p.event("proactive_skill_error")
		return
	}
	loc, _ := time.LoadLocation(proactiveTimezone)
	if decideProactive(OriginScheduled, result.Suppress || strings.TrimSpace(result.Text) == "", p.now(), loc) == DecisionSuppress {
		p.finishRun(run.ID, RunSkipped, boundedLabel(result.SuppressReason))
		p.event("proactive_suppressed")
		return
	}
	_, _, err = p.Outbox.Enqueue(ProactiveNotification{
		OperatorID: task.OperatorID, Channel: task.Channel, ConversationID: task.ConversationID,
		TaskID: task.ID, RunID: run.ID, ScheduledFor: run.ScheduledFor,
		Text: result.Text, Evidence: capEvidence(result.Evidence),
	})
	if err != nil {
		p.finishRun(run.ID, RunFailed, "enqueue_error")
		p.event("proactive_state_error")
		return
	}
	if err := p.Tasks.QueueRun(run.ID); err != nil {
		p.event("proactive_state_error")
		return
	}
	p.event("proactive_queued")
}

func mapNotificationStatus(status ProactiveStatus) ScheduledRunStatus {
	switch status {
	case ProactiveDelivered:
		return RunDelivered
	case ProactiveUncertain:
		return RunUncertain
	case ProactiveExpired:
		return RunSkipped
	}
	return RunFailed
}

// inSendWindow reports whether proactive messages may be sent at the instant.
func inSendWindow(now time.Time, loc *time.Location) bool {
	hour := now.In(loc).Hour()
	return hour >= proactiveWindowStartHour && hour < proactiveWindowEndHour
}

func (p *ProactiveScheduler) deliver(ctx context.Context) {
	if p.Sender == nil {
		return
	}
	pending, err := p.Outbox.Deliverable("telegram")
	if err != nil {
		p.event("proactive_state_error")
		return
	}
	if len(pending) == 0 {
		return
	}
	loc, err := time.LoadLocation(proactiveTimezone)
	if err != nil {
		p.event("proactive_state_error")
		return
	}
	now := p.now()
	byOperator := map[int64][]ProactiveNotification{}
	var order []int64
	for _, n := range pending {
		if _, seen := byOperator[n.OperatorID]; !seen {
			order = append(order, n.OperatorID)
		}
		byOperator[n.OperatorID] = append(byOperator[n.OperatorID], n)
	}
	for _, operator := range order {
		if ctx.Err() != nil {
			return
		}
		group := byOperator[operator]
		if decideProactive(OriginScheduled, false, now, loc) != DecisionSend {
			p.event("proactive_deferred_quiet_hours")
			continue
		}
		sent, err := p.Outbox.SentMessagesOn(operator, now, loc)
		if err != nil {
			p.event("proactive_state_error")
			continue
		}
		if sent >= proactiveDailyLimit {
			p.event("proactive_deferred_daily_limit")
			continue
		}
		p.sendGroup(ctx, operator, group, now)
	}
}

// sendGroup coalesces an operator's ready notifications into one message,
// persists the sending state, sends, and records the exact outcome.
func (p *ProactiveScheduler) sendGroup(ctx context.Context, operator int64, group []ProactiveNotification, now time.Time) {
	// Only the operator's own private chat is ever a destination.
	var private []ProactiveNotification
	for _, n := range group {
		if n.ConversationID == strconv.FormatInt(operator, 10) {
			private = append(private, n)
			continue
		}
		p.finishGroup([]ProactiveNotification{n}, ProactiveFailed, RunFailed, "not_private_chat", true)
		p.event("proactive_refused_destination")
	}
	if len(private) == 0 {
		return
	}
	group = private
	ids := make([]string, 0, len(group))
	for _, n := range group {
		ids = append(ids, n.ID)
	}
	text := coalesceNotificationText(group)
	if err := p.Outbox.BeginSend(ids, "g"+ids[0]); err != nil {
		p.event("proactive_state_error")
		return
	}
	outcome := p.Sender.Send(ctx, operator, text)
	switch outcome {
	case SendDelivered:
		p.finishGroup(group, ProactiveDelivered, RunDelivered, "", false)
		p.event("proactive_delivered")
	case SendFailed:
		exhausted := false
		for _, n := range group {
			if n.Attempts+1 >= proactiveMaxAttempts {
				exhausted = true
			}
		}
		p.finishGroup(group, ProactiveFailed, RunFailed, "send_failed", exhausted)
		p.event("proactive_send_failed")
	default:
		p.finishGroup(group, ProactiveUncertain, RunUncertain, "send_uncertain", true)
		p.event("proactive_send_uncertain")
	}
}

// finishGroup stores the outbox status and, when it is final, closes each run.
func (p *ProactiveScheduler) finishGroup(group []ProactiveNotification, status ProactiveStatus, runStatus ScheduledRunStatus, code string, final bool) {
	ids := make([]string, 0, len(group))
	for _, n := range group {
		ids = append(ids, n.ID)
	}
	if err := p.Outbox.Complete(ids, status); err != nil {
		p.event("proactive_state_error")
		return
	}
	if !final && status == ProactiveFailed {
		return // retried on a later pass; the run stays queued
	}
	for _, n := range group {
		p.finishRun(n.RunID, runStatus, code)
	}
}

// coalesceNotificationText merges related notifications into one message so a
// busy morning costs one slot of the daily limit, not several.
func coalesceNotificationText(group []ProactiveNotification) string {
	if len(group) == 1 {
		return group[0].Text
	}
	parts := make([]string, 0, len(group))
	for i, n := range group {
		parts = append(parts, fmt.Sprintf("%d) %s", i+1, n.Text))
	}
	return truncateTextRunes("Tienes "+strconv.Itoa(len(group))+" avisos programados:\n\n"+strings.Join(parts, "\n\n"), 3000)
}

// proactivePendingMessage renders up to three queued Open WebUI notifications for
// the next authenticated conversation, leaving the Granola flow untouched.
func (s *Service) proactivePendingMessage(operatorID int64) ([]ProactiveNotification, string, error) {
	if s.ProactiveOutbox == nil {
		return nil, "", nil
	}
	items, remaining, err := s.ProactiveOutbox.PendingOpenWebUI(operatorID, proactiveOpenWebUILimit)
	if err != nil || len(items) == 0 {
		return nil, "", err
	}
	texts := make([]string, 0, len(items)+1)
	for _, item := range items {
		texts = append(texts, item.Text)
	}
	if remaining > 0 {
		texts = append(texts, "Quedan "+strconv.Itoa(remaining)+" avisos programados por mostrar.")
	}
	return items, strings.Join(texts, "\n\n---\n\n"), nil
}

// acknowledgeProactive marks the shown notifications delivered and closes their
// runs. It runs only after the response reached the client.
func (s *Service) acknowledgeProactive(operatorID int64, items []ProactiveNotification) {
	if s.ProactiveOutbox == nil || len(items) == 0 {
		return
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.OperatorID == operatorID {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 || s.ProactiveOutbox.Complete(ids, ProactiveDelivered) != nil {
		return
	}
	for _, item := range items {
		if s.ScheduledTasks != nil && item.OperatorID == operatorID {
			s.ScheduledTasks.FinishRun(item.RunID, RunDelivered, "")
		}
		s.VoiceMetrics().ObserveProactive("proactive_shown_openwebui")
	}
}
