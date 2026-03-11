package issues

import (
	"strings"
	"testing"
)

func TestFormatEditableIssueOmitsRetiredFields(t *testing.T) {
	body := FormatEditableIssue(Issue{
		Title:       "Issue title",
		Type:        TypeTask,
		Status:      StatusOpen,
		Priority:    "medium",
		Assignee:    "alice",
		ParentID:    "tk-1",
		Labels:      []string{"backend", "cli"},
		Description: "details",
	})

	if strings.Contains(body, "deferred_until:") {
		t.Fatalf("expected deferred_until to be omitted, got %q", body)
	}

	if strings.Contains(body, "estimate_minutes:") {
		t.Fatalf("expected estimate_minutes to be omitted, got %q", body)
	}
}
