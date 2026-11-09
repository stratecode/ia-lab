package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/stratecode/lab/internal/secretary"
)

// parseCheckinsEnabled reads LAB_SECRETARY_CHECKINS_ENABLED. Hourly work
// check-ins are opt-in: empty means off, anything but true/false is a startup error.
func parseCheckinsEnabled(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, fmt.Errorf("check-ins setting must be true or false")
	}
}

// buildCheckins wires the work journal and its scheduler. With the flag off it
// returns nil and touches nothing: no store, no command, no goroutine. With it on
// it requires exactly one bound operator and Telegram, because questions only go
// to that operator's private chat.
func buildCheckins(enabledValue string, service *secretary.Service, sender secretary.CheckinSender, allowed map[int64]bool) (*secretary.CheckinScheduler, error) {
	enabled, err := parseCheckinsEnabled(enabledValue)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	var operator int64
	operators := 0
	for id, ok := range allowed {
		if ok {
			operators++
			operator = id
		}
	}
	if operators != 1 {
		return nil, errors.New("check-ins require exactly one bound operator")
	}
	if sender == nil {
		return nil, errors.New("check-ins require Telegram")
	}
	journal, err := secretary.OpenWorkJournal(service.StateDir)
	if err != nil {
		return nil, err
	}
	service.Journal = journal
	return &secretary.CheckinScheduler{Journal: journal, Sender: sender, ChatID: operator}, nil
}
