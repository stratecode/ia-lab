package httpapi

import (
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestSelectInitiativeTasksFiltersByTaskIDAndGroup(t *testing.T) {
	items := []domain.InitiativeTaskLinkResponse{
		{TaskID: "task-1", LaunchGroup: initiativeTestStringPtr("delivery")},
		{TaskID: "task-2", LaunchGroup: initiativeTestStringPtr("review")},
		{TaskID: "task-3", Epic: initiativeTestStringPtr("manual")},
	}

	byTask := selectInitiativeTasks(items, []string{"task-2"}, nil)
	if len(byTask) != 1 || byTask[0].TaskID != "task-2" {
		t.Fatalf("unexpected task-id filter result: %#v", byTask)
	}

	byGroup := selectInitiativeTasks(items, nil, []string{"delivery", "manual"})
	if len(byGroup) != 2 {
		t.Fatalf("unexpected group filter result: %#v", byGroup)
	}
}

func initiativeTestStringPtr(value string) *string { return &value }
