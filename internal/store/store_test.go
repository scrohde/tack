package store_test

import (
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

func TestReadyExcludesParentsWithOpenChildren(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	parent, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "parent",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	child, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "child",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    parent.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	standalone, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "standalone",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	ready, err := s.ReadyIssues(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(ready) != 2 || ready[0].ID != child.ID || ready[1].ID != standalone.ID {
		t.Fatalf("unexpected ready set with open child: %#v", ready)
	}

	listed, err := s.ListIssues(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) != 3 || listed[0].ID != parent.ID {
		t.Fatalf("expected parent to remain visible in list results, got %#v", listed)
	}

	exported, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(exported.Issues) != 3 || exported.Issues[0].ID != parent.ID {
		t.Fatalf("expected parent to remain visible in export, got %#v", exported.Issues)
	}

	_, err = s.CloseIssue(ctx, child.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	ready, err = s.ReadyIssues(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(ready) != 2 || ready[0].ID != parent.ID || ready[1].ID != standalone.ID {
		t.Fatalf("unexpected ready set after child closed: %#v", ready)
	}
}

func TestReadySummariesExcludeParentsWithOpenChildren(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	parent, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "parent",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	child, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "child",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    parent.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	standalone, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "standalone",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	ready, err := s.ReadyIssueSummaries(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(ready) != 2 || ready[0].ID != child.ID || ready[1].ID != standalone.ID {
		t.Fatalf("unexpected summary ready set with open child: %#v", ready)
	}

	_, err = s.CloseIssue(ctx, child.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	ready, err = s.ReadyIssueSummaries(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(ready) != 2 || ready[0].ID != parent.ID || ready[1].ID != standalone.ID {
		t.Fatalf("unexpected summary ready set after child closed: %#v", ready)
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
