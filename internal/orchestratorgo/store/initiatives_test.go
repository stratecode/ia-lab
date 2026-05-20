package store

import (
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestDeriveInitiativeExecutionStatus(t *testing.T) {
	tests := []struct {
		name    string
		current domain.InitiativeStatus
		items   []domain.InitiativeTaskLinkResponse
		want    domain.InitiativeStatus
	}{
		{
			name:    "manual created keeps initiative ready",
			current: domain.InitiativeStatusExecuting,
			items: []domain.InitiativeTaskLinkResponse{
				{ExecutionMode: domain.TaskLaunchModeManual, Task: domain.TaskResponse{State: domain.TaskStateCreated}},
			},
			want: domain.InitiativeStatusExecutionReady,
		},
		{
			name:    "running task keeps initiative executing",
			current: domain.InitiativeStatusExecutionReady,
			items: []domain.InitiativeTaskLinkResponse{
				{ExecutionMode: domain.TaskLaunchModeAgentLocal, Task: domain.TaskResponse{State: domain.TaskStateInProgress}},
			},
			want: domain.InitiativeStatusExecuting,
		},
		{
			name:    "failed task blocks initiative",
			current: domain.InitiativeStatusExecuting,
			items: []domain.InitiativeTaskLinkResponse{
				{ExecutionMode: domain.TaskLaunchModeAgentLocal, Task: domain.TaskResponse{State: domain.TaskStateFailed}},
			},
			want: domain.InitiativeStatusBlocked,
		},
		{
			name:    "completed launched tasks complete initiative",
			current: domain.InitiativeStatusExecuting,
			items: []domain.InitiativeTaskLinkResponse{
				{ExecutionMode: domain.TaskLaunchModeAgentLocal, Task: domain.TaskResponse{State: domain.TaskStateCompleted}},
				{ExecutionMode: domain.TaskLaunchModeAgentRemote, Task: domain.TaskResponse{State: domain.TaskStateCompleted}},
			},
			want: domain.InitiativeStatusCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveInitiativeExecutionStatus(tt.current, tt.items); got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}
