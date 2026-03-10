package store_test

import (
	"strings"
	"testing"
	"time"

	"tack/internal/issues"
	"tack/internal/store"
	"tack/internal/testutil"
)

func TestCreateIssueSequenceAndExport(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	first, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "first",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		Labels:      []string{"One", "two"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	second, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "second",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "high",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	if first.ID != "tk-1" || second.ID != "tk-2" {
		t.Fatalf("unexpected issue ids: %s %s", first.ID, second.ID)
	}

	if len(first.Labels) != 2 || first.Labels[0] != "one" || first.Labels[1] != "two" {
		t.Fatalf("unexpected labels: %#v", first.Labels)
	}

	exported, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(exported.Issues) != 2 {
		t.Fatalf("expected 2 issues in export, got %d", len(exported.Issues))
	}

	if len(exported.Events) != 2 {
		t.Fatalf("expected 2 create events, got %d", len(exported.Events))
	}

	if exported.Metadata["schema_version"] != "1" {
		t.Fatalf("unexpected schema version: %#v", exported.Metadata["schema_version"])
	}
}

func TestReadyFilteringAndCloseUnblocks(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	blocker, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocker",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blocked, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocked",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		DependsOn:   []string{blocker.ID},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	future := time.Now().UTC().Add(24 * time.Hour)

	_, err = s.CreateIssue(ctx, store.CreateIssueInput{
		Title:         "deferred",
		Description:   "body",
		Type:          issues.TypeTask,
		Priority:      "medium",
		DeferredUntil: &future,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "claimed",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.UpdateIssue(ctx, claimed.ID, store.UpdateIssueInput{Claim: true}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	ready, err := s.ReadyIssues(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(ready) != 1 || ready[0].ID != blocker.ID {
		t.Fatalf("unexpected ready set before close: %#v", ready)
	}

	_, err = s.CloseIssue(ctx, blocker.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	ready, err = s.ReadyIssues(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(ready) != 1 || ready[0].ID != blocked.ID {
		t.Fatalf("unexpected ready set after close: %#v", ready)
	}
}

func TestIssueSummariesIncludeCompactDependencyAndChildData(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	parent, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "parent",
		Description: "parent body",
		Type:        issues.TypeTask,
		Priority:    "high",
		Labels:      []string{"Backend", "api"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	childOpen, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "child open",
		Description: "child body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    parent.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	childClosed, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "child closed",
		Description: "child body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    parent.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blockerOpen, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocker open",
		Description: "blocker body",
		Type:        issues.TypeBug,
		Priority:    "urgent",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blockerClosed, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocker closed",
		Description: "blocker body",
		Type:        issues.TypeTask,
		Priority:    "low",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blocked, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocked",
		Description: "blocked body",
		Type:        issues.TypeFeature,
		Priority:    "medium",
		DependsOn:   []string{blockerOpen.ID, blockerClosed.ID},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CloseIssue(ctx, childClosed.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CloseIssue(ctx, blockerClosed.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	summaries, err := s.ListIssueSummaries(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	byID := make(map[string]issues.IssueSummary, len(summaries))
	for _, summary := range summaries {
		byID[summary.ID] = summary
	}

	parentSummary := byID[parent.ID]
	if got := strings.Join(parentSummary.Labels, ","); got != "api,backend" {
		t.Fatalf("unexpected labels: %#v", parentSummary)
	}

	if got := strings.Join(parentSummary.OpenChildren, ","); got != childOpen.ID {
		t.Fatalf("unexpected open children: %#v", parentSummary)
	}

	if len(parentSummary.BlockedBy) != 0 {
		t.Fatalf("expected no blockers for parent summary: %#v", parentSummary)
	}

	blockedSummary := byID[blocked.ID]
	if got := strings.Join(blockedSummary.BlockedBy, ","); got != blockerOpen.ID {
		t.Fatalf("unexpected blocked_by: %#v", blockedSummary)
	}

	if blockedSummary.Title != "blocked" || blockedSummary.Type != issues.TypeFeature {
		t.Fatalf("unexpected base summary fields: %#v", blockedSummary)
	}

	readySummaries, err := s.ReadyIssueSummaries(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	for _, summary := range readySummaries {
		if summary.ID == blocked.ID {
			t.Fatalf("blocked issue should not be ready: %#v", readySummaries)
		}
	}
}

func TestDependencyCycleRejected(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	a, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "A",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	b, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "B",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddDependency(ctx, b.ID, a.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddDependency(ctx, a.ID, b.ID, "alice")
	if err == nil {
		t.Fatal("expected cycle rejection")
	}
}

func TestCommentsLabelsAndEvents(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	issue, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "issue",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddComment(ctx, issue.ID, "note", "alice")
	if err != nil {
		t.Fatal(err)
	}

	labels, err := s.AddLabels(ctx, issue.ID, []string{"backend", "urgent"}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	if len(labels) != 2 {
		t.Fatalf("expected labels, got %#v", labels)
	}

	comments, err := s.ListComments(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(comments) != 1 || comments[0].Body != "note" {
		t.Fatalf("unexpected comments: %#v", comments)
	}

	exported, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(exported.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(exported.Comments))
	}

	if len(exported.Events) != 3 {
		t.Fatalf("expected create/comment/labels events, got %d", len(exported.Events))
	}
}
