package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/stratecode/lab/internal/secretary"
)

// parseProactiveEnabled reads LAB_SECRETARY_PROACTIVE_ENABLED. Scheduled skills
// and proactive messages are opt-in: empty means off, anything but true/false is
// a startup error rather than a silent default.
func parseProactiveEnabled(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("proactive setting must be true or false")
	}
}

// buildProactive wires the scheduled-skill feature. With the flag off it returns
// a nil scheduler and leaves the service untouched, so no store is opened, no
// command is active and no goroutine can start. With the flag on it refuses to
// start unless exactly one operator is bound: schedules are only ever delivered
// to that operator, never to a recipient chosen elsewhere.
//
// sender is the Telegram adapter, or nil when Telegram is disabled; Open WebUI
// notices are pulled on the next authenticated conversation and need no sender.
func buildProactive(enabledValue string, service *secretary.Service, sender secretary.ProactiveSender, allowed map[int64]bool, event func(string)) (*secretary.ProactiveScheduler, error) {
	enabled, err := parseProactiveEnabled(enabledValue)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	operators := 0
	for _, ok := range allowed {
		if ok {
			operators++
		}
	}
	if operators != 1 {
		return nil, errors.New("proactive messaging requires exactly one bound operator")
	}
	tasks, err := secretary.OpenScheduledTaskStore(service.StateDir)
	if err != nil {
		return nil, err
	}
	outbox, err := secretary.OpenProactiveOutbox(service.StateDir)
	if err != nil {
		return nil, err
	}
	service.ScheduledTasks, service.ProactiveOutbox = tasks, outbox
	scheduler := &secretary.ProactiveScheduler{
		Tasks: tasks, Outbox: outbox,
		Executor: secretary.SkillExecutor{Runner: secretary.NewScheduledSkillRunner(service)},
		Event:    event, Metrics: service.VoiceMetrics(),
	}
	if sender != nil {
		scheduler.Sender = sender
	}
	return scheduler, nil
}
