package store_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

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

func TestListFiltersSupportMultipleValues(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	issueOne, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "open task",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		Labels:      []string{"api"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	issueTwo, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocked bug",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"api"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	issueThree, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "open ops bug",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"ops"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	issueFour, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "open docs bug",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"docs"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	aliceAssignee := "alice"
	bobAssignee := "bob"
	blockedStatus := issues.StatusBlocked

	for _, update := range []struct {
		id       string
		assignee *string
		status   *string
	}{
		{id: issueOne.ID, assignee: &aliceAssignee},
		{id: issueTwo.ID, assignee: &bobAssignee, status: &blockedStatus},
		{id: issueThree.ID, assignee: &aliceAssignee},
		{id: issueFour.ID, assignee: &aliceAssignee},
	} {
		_, err = s.UpdateIssue(ctx, update.id, store.UpdateIssueInput{
			Assignee: update.assignee,
			Status:   update.status,
		}, "alice")
		if err != nil {
			t.Fatal(err)
		}
	}

	filter := store.ListFilter{
		Statuses:  []string{issues.StatusOpen, issues.StatusBlocked},
		Types:     []string{issues.TypeBug},
		Labels:    []string{"api", "ops"},
		Assignees: []string{"alice", "bob"},
	}

	listed, err := s.ListIssues(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) != 2 || listed[0].ID != issueTwo.ID || listed[1].ID != issueThree.ID {
		t.Fatalf("unexpected multi-value list results: %#v", listed)
	}

	summaries, err := s.ListIssueSummaries(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}

	if len(summaries) != 2 || summaries[0].ID != issueTwo.ID || summaries[1].ID != issueThree.ID {
		t.Fatalf("unexpected multi-value summary results: %#v", summaries)
	}
}

func TestReadyFiltersSupportMultipleValues(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	readyTask, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "ready task",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		Labels:      []string{"backend"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	readyBug, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "ready bug",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"ui"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blockedBug, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocked bug",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"ui"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	claimedBug, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "claimed bug",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"ui"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blockedStatus := issues.StatusBlocked

	_, err = s.UpdateIssue(ctx, blockedBug.ID, store.UpdateIssueInput{Status: &blockedStatus}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.UpdateIssue(ctx, claimedBug.ID, store.UpdateIssueInput{Claim: true}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	filter := store.ListFilter{
		Statuses: []string{issues.StatusOpen, issues.StatusBlocked},
		Types:    []string{issues.TypeTask, issues.TypeBug},
		Labels:   []string{"backend", "ui"},
	}

	ready, err := s.ReadyIssues(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}

	if len(ready) != 2 || ready[0].ID != readyTask.ID || ready[1].ID != readyBug.ID {
		t.Fatalf("unexpected multi-value ready results: %#v", ready)
	}

	summaries, err := s.ReadyIssueSummaries(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}

	if len(summaries) != 2 || summaries[0].ID != readyTask.ID || summaries[1].ID != readyBug.ID {
		t.Fatalf("unexpected multi-value ready summaries: %#v", summaries)
	}
}

func TestListFilterValuesRespectOtherFilters(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	apiBug, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "api bug",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"api"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	opsBug, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "ops bug",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"ops"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	closedTask, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "closed task",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		Labels:      []string{"api"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	bobAssignee := "bob"
	closedStatus := issues.StatusClosed

	for _, issue := range []string{apiBug.ID, opsBug.ID} {
		_, err = s.UpdateIssue(ctx, issue, store.UpdateIssueInput{Assignee: &bobAssignee}, "alice")
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = s.UpdateIssue(ctx, closedTask.ID, store.UpdateIssueInput{Status: &closedStatus}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	filter := store.ListFilter{
		Statuses:  []string{issues.StatusOpen},
		Types:     []string{issues.TypeBug},
		Labels:    []string{"api"},
		Assignees: []string{"bob"},
	}

	statusValues, err := s.ListFilterValues(ctx, store.FilterValueSourceAll, store.FilterValueKeyStatus, filter)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(statusValues, []string{issues.StatusOpen}) {
		t.Fatalf("unexpected status values: %#v", statusValues)
	}

	typeValues, err := s.ListFilterValues(ctx, store.FilterValueSourceAll, store.FilterValueKeyType, filter)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(typeValues, []string{issues.TypeBug}) {
		t.Fatalf("unexpected type values: %#v", typeValues)
	}

	labelValues, err := s.ListFilterValues(ctx, store.FilterValueSourceAll, store.FilterValueKeyLabel, filter)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(labelValues, []string{"api", "ops"}) {
		t.Fatalf("unexpected label values: %#v", labelValues)
	}

	assigneeValues, err := s.ListFilterValues(ctx, store.FilterValueSourceAll, store.FilterValueKeyAssignee, filter)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(assigneeValues, []string{"bob"}) {
		t.Fatalf("unexpected assignee values: %#v", assigneeValues)
	}
}

func TestReadyFilterValuesRespectReadySource(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	readyOne, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "ready one",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		Labels:      []string{"ui"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	readyTwo, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "ready two",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"backend"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blocked, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocked",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"ui"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "claimed",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"ui"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blockedStatus := issues.StatusBlocked

	_, err = s.UpdateIssue(ctx, blocked.ID, store.UpdateIssueInput{Status: &blockedStatus}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.UpdateIssue(ctx, claimed.ID, store.UpdateIssueInput{Claim: true}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	filter := store.ListFilter{
		Statuses: []string{issues.StatusOpen, issues.StatusBlocked},
		Types:    []string{issues.TypeTask, issues.TypeBug},
		Labels:   []string{"ui"},
	}

	labelValues, err := s.ListFilterValues(ctx, store.FilterValueSourceReady, store.FilterValueKeyLabel, filter)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(labelValues, []string{"backend", "ui"}) {
		t.Fatalf("unexpected ready label values: %#v", labelValues)
	}

	statusValues, err := s.ListFilterValues(ctx, store.FilterValueSourceReady, store.FilterValueKeyStatus, filter)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(statusValues, []string{issues.StatusOpen}) {
		t.Fatalf("unexpected ready status values: %#v", statusValues)
	}

	assigneeValues, err := s.ListFilterValues(ctx, store.FilterValueSourceReady, store.FilterValueKeyAssignee, filter)
	if err != nil {
		t.Fatal(err)
	}

	if len(assigneeValues) != 0 {
		t.Fatalf("expected no ready assignee values, got %#v", assigneeValues)
	}

	typeValues, err := s.ListFilterValues(ctx, store.FilterValueSourceReady, store.FilterValueKeyType, store.ListFilter{
		Labels: []string{"ui", "backend"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(typeValues, []string{issues.TypeTask, issues.TypeBug}) {
		t.Fatalf("unexpected ready type values: %#v", typeValues)
	}

	if readyOne.ID == readyTwo.ID {
		t.Fatal("expected distinct ready issues")
	}
}

func TestIssueDetailViewIncludesCommentsDependenciesAndRelatedSummaries(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	parent, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "parent",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		Labels:      []string{"planning"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blocker, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocker",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "high",
		Labels:      []string{"backend"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	target, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "target",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    parent.ID,
		DependsOn:   []string{blocker.ID},
		Labels:      []string{"tui"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	downstream, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "downstream",
		Description: "body",
		Type:        issues.TypeFeature,
		Priority:    "medium",
		DependsOn:   []string{target.ID},
		Labels:      []string{"frontend"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddComment(ctx, target.ID, "needs graph context", "alice")
	if err != nil {
		t.Fatal(err)
	}

	view, err := s.IssueDetailView(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}

	if view.Issue.ID != target.ID {
		t.Fatalf("unexpected issue: %#v", view.Issue)
	}

	if len(view.Comments) != 1 || view.Comments[0].Body != "needs graph context" {
		t.Fatalf("unexpected comments: %#v", view.Comments)
	}

	if len(view.Events) != 2 {
		t.Fatalf("unexpected events: %#v", view.Events)
	}

	if view.Events[0].EventType != "issue_created" || view.Events[1].EventType != "comment_added" {
		t.Fatalf("expected chronological events, got %#v", view.Events)
	}

	if len(view.Dependencies.BlockedBy) != 1 || view.Dependencies.BlockedBy[0].SourceID != blocker.ID {
		t.Fatalf("unexpected blockers: %#v", view.Dependencies.BlockedBy)
	}

	if len(view.Dependencies.Blocks) != 1 || view.Dependencies.Blocks[0].TargetID != downstream.ID {
		t.Fatalf("unexpected blocked issues: %#v", view.Dependencies.Blocks)
	}

	if len(view.RelatedSummaries) != 3 {
		t.Fatalf("unexpected related summaries: %#v", view.RelatedSummaries)
	}

	if view.RelatedSummaries[parent.ID].Title != parent.Title {
		t.Fatalf("missing parent summary: %#v", view.RelatedSummaries)
	}

	if labels := view.RelatedSummaries[blocker.ID].Labels; len(labels) != 1 || labels[0] != "backend" {
		t.Fatalf("unexpected blocker summary labels: %#v", view.RelatedSummaries[blocker.ID])
	}

	if view.RelatedSummaries[downstream.ID].Title != downstream.Title {
		t.Fatalf("missing downstream summary: %#v", view.RelatedSummaries)
	}

	if view.LatestCloseReason != "" || view.LatestReopenReason != "" {
		t.Fatalf("expected empty transition reasons for untouched issue, got close=%q reopen=%q", view.LatestCloseReason, view.LatestReopenReason)
	}
}

func TestIssueDetailViewIncludesRepresentativeEventHistory(t *testing.T) {
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

	target, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "target",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddLabels(ctx, target.ID, []string{"ui"}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	status := issues.StatusInProgress
	assignee := "alice"

	_, err = s.UpdateIssue(ctx, target.ID, store.UpdateIssueInput{
		Status:   &status,
		Assignee: &assignee,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddDependency(ctx, target.ID, blocker.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CloseIssue(ctx, target.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.ReopenIssue(ctx, target.ID, "follow-up requested", "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.ReplaceLabels(ctx, target.ID, []string{"backend", "tui"}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	err = s.RemoveDependency(ctx, target.ID, blocker.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	view, err := s.IssueDetailView(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}

	var gotTypes []string

	for _, event := range view.Events {
		if event.IssueID != target.ID {
			t.Fatalf("expected detail-view event for %s, got %#v", target.ID, event)
		}

		gotTypes = append(gotTypes, event.EventType)
	}

	wantTypes := []string{
		"issue_created",
		"labels_added",
		"issue_updated",
		"dependency_added",
		"issue_closed",
		"issue_reopened",
		"labels_replaced",
		"dependency_removed",
	}
	if !slices.Equal(gotTypes, wantTypes) {
		t.Fatalf("unexpected event history: %#v", gotTypes)
	}

	if view.LatestCloseReason != "done" || view.LatestReopenReason != "follow-up requested" {
		t.Fatalf("unexpected transition reasons: close=%q reopen=%q", view.LatestCloseReason, view.LatestReopenReason)
	}
}

func TestIssueDetailViewInitializesEmptyCollections(t *testing.T) {
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

	view, err := s.IssueDetailView(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	if view.Comments == nil || view.Events == nil || view.Dependencies.BlockedBy == nil || view.Dependencies.Blocks == nil {
		t.Fatalf("expected initialized detail-view slices, got %#v", view)
	}

	if len(view.Comments) != 0 || len(view.Events) != 1 || len(view.Dependencies.BlockedBy) != 0 || len(view.Dependencies.Blocks) != 0 {
		t.Fatalf("unexpected detail-view collections: %#v", view)
	}
}

func TestIssueDetailViewDerivesLatestTransitionReasons(t *testing.T) {
	t.Parallel()

	type transition struct {
		kind   string
		reason string
	}

	testCases := []struct {
		name        string
		transitions []transition
		wantClose   string
		wantReopen  string
	}{
		{
			name: "close reason only",
			transitions: []transition{
				{kind: "close", reason: "done"},
			},
			wantClose: "done",
		},
		{
			name: "reopen reason only",
			transitions: []transition{
				{kind: "close", reason: ""},
				{kind: "reopen", reason: "needed more work"},
			},
			wantReopen: "needed more work",
		},
		{
			name: "both reasons",
			transitions: []transition{
				{kind: "close", reason: "done"},
				{kind: "reopen", reason: "follow-up requested"},
			},
			wantClose:  "done",
			wantReopen: "follow-up requested",
		},
		{
			name: "blank reasons stay omitted",
			transitions: []transition{
				{kind: "close", reason: ""},
				{kind: "reopen", reason: " "},
			},
		},
		{
			name: "repeated transitions use latest non-empty reason",
			transitions: []transition{
				{kind: "close", reason: ""},
				{kind: "reopen", reason: "needed more work"},
				{kind: "close", reason: "initial fix landed"},
				{kind: "reopen", reason: " "},
				{kind: "close", reason: "verified and done"},
			},
			wantClose:  "verified and done",
			wantReopen: "needed more work",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testutil.Context(t)
			repo := testutil.TempRepo(t)
			s := testutil.InitStore(t, repo)

			target, err := s.CreateIssue(ctx, store.CreateIssueInput{
				Title:       "target",
				Description: "body",
				Type:        issues.TypeTask,
				Priority:    "medium",
			}, "alice")
			if err != nil {
				t.Fatal(err)
			}

			for _, step := range tc.transitions {
				switch step.kind {
				case "close":
					_, err = s.CloseIssue(ctx, target.ID, step.reason, "alice")
				case "reopen":
					_, err = s.ReopenIssue(ctx, target.ID, step.reason, "alice")
				default:
					t.Fatalf("unknown transition kind %q", step.kind)
				}

				if err != nil {
					t.Fatal(err)
				}
			}

			view, err := s.IssueDetailView(ctx, target.ID)
			if err != nil {
				t.Fatal(err)
			}

			if view.LatestCloseReason != tc.wantClose {
				t.Fatalf("unexpected latest close reason: got %q want %q", view.LatestCloseReason, tc.wantClose)
			}

			if view.LatestReopenReason != tc.wantReopen {
				t.Fatalf("unexpected latest reopen reason: got %q want %q", view.LatestReopenReason, tc.wantReopen)
			}
		})
	}
}

func TestFocusedGraphViewIncludesDirectNeighborhoodOnly(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	grandparent, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "grandparent",
		Description: "body",
		Type:        issues.TypeEpic,
		Priority:    "high",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	parent, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "parent",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    grandparent.ID,
		Labels:      []string{"planning"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blockerOpen, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocker-open",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "high",
		Labels:      []string{"backend"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blockerClosed, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocker-closed",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "medium",
		Labels:      []string{"ops"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	focus, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "focus",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    parent.ID,
		DependsOn:   []string{blockerOpen.ID, blockerClosed.ID},
		Labels:      []string{"tui"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blockedOpen, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocked-open",
		Description: "body",
		Type:        issues.TypeFeature,
		Priority:    "medium",
		DependsOn:   []string{focus.ID},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blockedClosed, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocked-closed",
		Description: "body",
		Type:        issues.TypeFeature,
		Priority:    "low",
		DependsOn:   []string{focus.ID},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	childOpen, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "child-open",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    focus.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	childClosed, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "child-closed",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    focus.ID,
		Labels:      []string{"docs"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "grandchild",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    childOpen.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "unrelated",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CloseIssue(ctx, blockerClosed.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CloseIssue(ctx, blockedClosed.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CloseIssue(ctx, childClosed.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	view, err := s.FocusedGraphView(ctx, focus.ID)
	if err != nil {
		t.Fatal(err)
	}

	if view.SelectedID != focus.ID {
		t.Fatalf("unexpected selected id: %#v", view)
	}

	if view.ParentID != parent.ID {
		t.Fatalf("unexpected parent id: %#v", view)
	}

	if !reflect.DeepEqual(view.BlockedByIDs, []string{blockerOpen.ID, blockerClosed.ID}) {
		t.Fatalf("unexpected blockers: %#v", view.BlockedByIDs)
	}

	if !reflect.DeepEqual(view.BlocksIDs, []string{blockedOpen.ID, blockedClosed.ID}) {
		t.Fatalf("unexpected blocked issues: %#v", view.BlocksIDs)
	}

	if !reflect.DeepEqual(view.ChildIDs, []string{childOpen.ID, childClosed.ID}) {
		t.Fatalf("unexpected child ids: %#v", view.ChildIDs)
	}

	if len(view.NodeSummaries) != 8 {
		t.Fatalf("unexpected node summaries: %#v", view.NodeSummaries)
	}

	if _, ok := view.NodeSummaries[grandparent.ID]; ok {
		t.Fatalf("grandparent should not be included in focused graph: %#v", view.NodeSummaries)
	}

	if labels := view.NodeSummaries[blockerOpen.ID].Labels; len(labels) != 1 || labels[0] != "backend" {
		t.Fatalf("unexpected blocker summary: %#v", view.NodeSummaries[blockerOpen.ID])
	}

	if view.NodeSummaries[focus.ID].Status != issues.StatusOpen {
		t.Fatalf("unexpected focus status: %#v", view.NodeSummaries[focus.ID])
	}

	if view.NodeSummaries[blockerClosed.ID].Status != issues.StatusClosed {
		t.Fatalf("expected closed blocker summary: %#v", view.NodeSummaries[blockerClosed.ID])
	}

	if view.NodeSummaries[blockedClosed.ID].Status != issues.StatusClosed {
		t.Fatalf("expected closed blocked issue summary: %#v", view.NodeSummaries[blockedClosed.ID])
	}

	if view.NodeSummaries[childClosed.ID].Status != issues.StatusClosed {
		t.Fatalf("expected closed child summary: %#v", view.NodeSummaries[childClosed.ID])
	}
}

func TestProjectGraphViewIncludesAllIssueSummariesAndGraphLinks(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	parent, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "parent",
		Description: "body",
		Type:        issues.TypeEpic,
		Priority:    "high",
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

	blocker, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocker",
		Description: "body",
		Type:        issues.TypeBug,
		Priority:    "high",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blocked, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "blocked",
		Description: "body",
		Type:        issues.TypeFeature,
		Priority:    "medium",
		DependsOn:   []string{child.ID},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	leaf, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "leaf",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "low",
		DependsOn:   []string{blocker.ID},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddDependency(ctx, child.ID, blocker.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CloseIssue(ctx, blocker.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	view, err := s.ProjectGraphView(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(view.Issues) != 5 {
		t.Fatalf("expected all issues in project graph, got %#v", view.Issues)
	}

	if len(view.Links) != 4 {
		t.Fatalf("expected graph links, got %#v", view.Links)
	}

	if view.Issues[2].ID != blocker.ID || view.Issues[2].Status != issues.StatusClosed {
		t.Fatalf("expected closed blocker summary in project graph: %#v", view.Issues)
	}

	gotLinks := []string{}
	for _, link := range view.Links {
		gotLinks = append(gotLinks, link.Kind+":"+link.SourceID+"->"+link.TargetID)
	}

	wantLinks := []string{
		"blocks:" + blocker.ID + "->" + child.ID,
		"blocks:" + blocker.ID + "->" + leaf.ID,
		"blocks:" + child.ID + "->" + blocked.ID,
		"parent_child:" + parent.ID + "->" + child.ID,
	}

	slices.Sort(gotLinks)
	slices.Sort(wantLinks)

	if !reflect.DeepEqual(gotLinks, wantLinks) {
		t.Fatalf("unexpected project graph links: got %#v want %#v", gotLinks, wantLinks)
	}
}

func TestReadyRejectsAssigneeFilters(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	_, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "issue",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	filter := store.ListFilter{Assignees: []string{" ", "alice", "bob", "alice"}}

	_, err = s.ReadyIssues(ctx, filter)
	if err == nil || !strings.Contains(err.Error(), "do not support assignee filters") {
		t.Fatalf("expected ready issue assignee filter rejection, got %v", err)
	}

	_, err = s.ReadyIssueSummaries(ctx, filter)
	if err == nil || !strings.Contains(err.Error(), "do not support assignee filters") {
		t.Fatalf("expected ready summary assignee filter rejection, got %v", err)
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

func TestUpdateIssueRejectsParentCycles(t *testing.T) {
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

	_, err = s.UpdateIssue(ctx, parent.ID, store.UpdateIssueInput{ParentID: &child.ID}, "alice")
	if err == nil || !strings.Contains(err.Error(), "parent cycle detected") {
		t.Fatalf("expected parent cycle error, got %v", err)
	}

	reloaded, err := s.GetIssue(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}

	if reloaded.ParentID != "" {
		t.Fatalf("expected parent to remain unset after rejected update, got %#v", reloaded)
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

func TestGetIssueDetailIncludesRelatedData(t *testing.T) {
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

	issue, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "issue",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		DependsOn:   []string{blocker.ID},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "downstream",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
		DependsOn:   []string{issue.ID},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddComment(ctx, issue.ID, "note", "alice")
	if err != nil {
		t.Fatal(err)
	}

	detail, err := s.GetIssueDetail(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(detail.Comments) != 1 || detail.Comments[0].Body != "note" {
		t.Fatalf("unexpected comments: %#v", detail.Comments)
	}

	if len(detail.BlockedBy) != 1 || detail.BlockedBy[0].SourceID != blocker.ID || detail.BlockedBy[0].TargetID != issue.ID {
		t.Fatalf("unexpected blocked_by links: %#v", detail.BlockedBy)
	}

	if len(detail.Blocks) != 1 || detail.Blocks[0].SourceID != issue.ID {
		t.Fatalf("unexpected blocks links: %#v", detail.Blocks)
	}

	if len(detail.Events) != 2 || detail.Events[1].EventType != "comment_added" {
		t.Fatalf("unexpected events: %#v", detail.Events)
	}
}

func TestGetIssueDetailIncludesEmptyCollections(t *testing.T) {
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

	detail, err := s.GetIssueDetail(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}

	if detail.Comments == nil || detail.BlockedBy == nil || detail.Blocks == nil || detail.Events == nil {
		t.Fatalf("expected initialized detail slices, got %#v", detail)
	}

	if len(detail.Comments) != 0 || len(detail.BlockedBy) != 0 || len(detail.Blocks) != 0 || len(detail.Events) != 1 {
		t.Fatalf("unexpected detail collections: %#v", detail)
	}
}

func TestImportIssuesCreatesGraph(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	result, err := s.ImportIssues(ctx, store.ImportManifest{
		Issues: []store.ImportIssueInput{
			{
				ID:          "epic",
				Title:       "Imported epic",
				Type:        issues.TypeEpic,
				Priority:    "high",
				Description: "parent issue",
				Labels:      []string{"Planning"},
			},
			{
				ID:          "task",
				Title:       "Imported task",
				Description: "child issue",
				Parent:      "epic",
				DependsOn:   []string{"bug"},
				Labels:      []string{"Backend", "backend"},
			},
			{
				ID:          "bug",
				Title:       "Imported bug",
				Type:        issues.TypeBug,
				Priority:    "urgent",
				Description: "blocking issue",
			},
		},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	if len(result.CreatedIDs) != 3 {
		t.Fatalf("expected 3 created issues, got %#v", result)
	}

	if got := result.AliasMap["epic"]; got != "tk-1" {
		t.Fatalf("unexpected epic id: %#v", result.AliasMap)
	}

	task, err := s.GetIssue(ctx, result.AliasMap["task"])
	if err != nil {
		t.Fatal(err)
	}

	if task.ParentID != result.AliasMap["epic"] {
		t.Fatalf("unexpected imported parent id: %#v", task)
	}

	if task.Type != issues.TypeTask || task.Priority != "medium" {
		t.Fatalf("expected default type/priority for imported task, got %#v", task)
	}

	if len(task.Labels) != 1 || task.Labels[0] != "backend" {
		t.Fatalf("unexpected imported task labels: %#v", task.Labels)
	}

	deps, err := s.ListDependencies(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps.BlockedBy) != 1 || deps.BlockedBy[0].SourceID != result.AliasMap["bug"] {
		t.Fatalf("unexpected imported dependencies: %#v", deps)
	}

	exported, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(exported.Events) != 3 {
		t.Fatalf("expected 3 create events after import, got %d", len(exported.Events))
	}
}

func TestImportIssuesRejectsDuplicateAlias(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	_, err := s.ImportIssues(ctx, store.ImportManifest{
		Issues: []store.ImportIssueInput{
			{ID: "dup", Title: "first"},
			{ID: "dup", Title: "second"},
		},
	}, "alice")
	if err == nil || err.Error() != `duplicate manifest issue id "dup"` {
		t.Fatalf("expected duplicate alias error, got %v", err)
	}
}

func TestImportIssuesRejectsMissingReference(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	_, err := s.ImportIssues(ctx, store.ImportManifest{
		Issues: []store.ImportIssueInput{
			{
				ID:        "task",
				Title:     "task",
				DependsOn: []string{"missing"},
			},
		},
	}, "alice")
	if err == nil || err.Error() != `issue "task" depends on unknown alias "missing"` {
		t.Fatalf("expected missing reference error, got %v", err)
	}
}

func TestImportIssuesRejectsParentCycles(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	_, err := s.ImportIssues(ctx, store.ImportManifest{
		Issues: []store.ImportIssueInput{
			{
				ID:     "a",
				Title:  "A",
				Parent: "b",
			},
			{
				ID:     "b",
				Title:  "B",
				Parent: "a",
			},
		},
	}, "alice")
	if err == nil || !strings.Contains(err.Error(), "parent cycle detected") {
		t.Fatalf("expected parent cycle error, got %v", err)
	}

	listed, err := s.ListIssues(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) != 0 {
		t.Fatalf("expected import rollback on parent cycle, got %#v", listed)
	}
}

func TestImportIssuesRollsBackOnInvalidGraph(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	_, err := s.ImportIssues(ctx, store.ImportManifest{
		Issues: []store.ImportIssueInput{
			{
				ID:        "a",
				Title:     "A",
				DependsOn: []string{"b"},
			},
			{
				ID:        "b",
				Title:     "B",
				DependsOn: []string{"a"},
			},
		},
	}, "alice")
	if err == nil || !strings.Contains(err.Error(), "dependency cycle detected") {
		t.Fatalf("expected cycle error, got %v", err)
	}

	listed, err := s.ListIssues(ctx, store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) != 0 {
		t.Fatalf("expected import rollback, got %#v", listed)
	}
}

func TestImportSnapshotRoundTripsExport(t *testing.T) {
	ctx := testutil.Context(t)
	sourceRepo := testutil.TempRepo(t)
	source := testutil.InitStore(t, sourceRepo)

	parent, err := source.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Parent epic",
		Description: "parent",
		Type:        issues.TypeEpic,
		Priority:    "high",
		Labels:      []string{"plan"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	blocker, err := source.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Blocking bug",
		Description: "blocker",
		Type:        issues.TypeBug,
		Priority:    "urgent",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	child, err := source.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Imported child",
		Description: "child",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    parent.ID,
		DependsOn:   []string{blocker.ID},
		Labels:      []string{"backend"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = source.UpdateIssue(ctx, child.ID, store.UpdateIssueInput{Claim: true}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = source.AddComment(ctx, child.ID, "note", "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = source.AddLabels(ctx, child.ID, []string{"urgent"}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = source.CloseIssue(ctx, blocker.ID, "fixed", "alice")
	if err != nil {
		t.Fatal(err)
	}

	exported, err := source.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}

	targetRepo := testutil.TempRepo(t)
	target := testutil.InitStore(t, targetRepo)

	result, err := target.ImportSnapshot(ctx, exported)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.CreatedIDs) != len(exported.Issues) {
		t.Fatalf("expected %d created ids, got %#v", len(exported.Issues), result.CreatedIDs)
	}

	if result.AliasMap[child.ID] != child.ID {
		t.Fatalf("expected identity alias map for snapshot import, got %#v", result.AliasMap)
	}

	roundTripped, err := target.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if roundTripped.Metadata["source_db_path"] != exported.Metadata["db_path"] {
		t.Fatalf("expected source_db_path to preserve original export path, got %#v", roundTripped.Metadata)
	}

	if !reflect.DeepEqual(exportWithoutDBPath(roundTripped), exportWithoutDBPath(exported)) {
		t.Fatalf("snapshot round-trip mismatch:\nwant: %#v\ngot:  %#v", exportWithoutDBPath(exported), exportWithoutDBPath(roundTripped))
	}
}

func TestAddDependencyDuplicateAddIsIdempotent(t *testing.T) {
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
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	first, err := s.AddDependency(ctx, blocked.ID, blocker.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	second, err := s.AddDependency(ctx, blocked.ID, blocker.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	if first.ID != second.ID || !first.CreatedAt.Equal(second.CreatedAt) {
		t.Fatalf("expected duplicate add to return existing link, got first=%#v second=%#v", first, second)
	}

	deps, err := s.ListDependencies(ctx, blocked.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps.BlockedBy) != 1 || deps.BlockedBy[0].ID != first.ID {
		t.Fatalf("expected a single stored dependency, got %#v", deps)
	}

	exported, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(exported.Links) != 1 {
		t.Fatalf("expected one dependency link in export, got %#v", exported.Links)
	}

	if got := countEventsByType(exported.Events, "dependency_added"); got != 1 {
		t.Fatalf("expected one dependency_added event, got %d", got)
	}
}

func TestRemoveDependencyMissingLinkIsNoOp(t *testing.T) {
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
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	err = s.RemoveDependency(ctx, blocked.ID, blocker.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddDependency(ctx, blocked.ID, blocker.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	err = s.RemoveDependency(ctx, blocked.ID, blocker.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	err = s.RemoveDependency(ctx, blocked.ID, blocker.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	deps, err := s.ListDependencies(ctx, blocked.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(deps.BlockedBy) != 0 {
		t.Fatalf("expected dependency list to be empty, got %#v", deps)
	}

	exported, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if got := countEventsByType(exported.Events, "dependency_removed"); got != 1 {
		t.Fatalf("expected one dependency_removed event, got %d", got)
	}
}

func countEventsByType(events []issues.Event, eventType string) int {
	count := 0

	for _, event := range events {
		if event.EventType == eventType {
			count++
		}
	}

	return count
}

func exportWithoutDBPath(data issues.Export) issues.Export {
	out := data
	out.Metadata = make(map[string]any, len(data.Metadata))

	for key, value := range data.Metadata {
		if key == "db_path" || key == "source_db_path" {
			continue
		}

		out.Metadata[key] = value
	}

	return out
}
