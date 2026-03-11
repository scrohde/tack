package store_test

import (
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

	_, err = s.ReadyIssues(ctx, store.ListFilter{Assignee: "alice"})
	if err == nil || !strings.Contains(err.Error(), "do not support assignee filters") {
		t.Fatalf("expected ready issue assignee filter rejection, got %v", err)
	}

	_, err = s.ReadyIssueSummaries(ctx, store.ListFilter{Assignee: "alice"})
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
