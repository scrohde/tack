package store_test

import (
	"testing"

	"tack/internal/issues"
	"tack/internal/store"
	"tack/internal/testutil"
)

func TestExportJiraExportsRequestedEpicSubtree(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	epic, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Requested epic",
		Description: "Top-level migration",
		Type:        issues.TypeEpic,
		Priority:    "high",
		Labels:      []string{"planning"},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	task, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Build exporter",
		Description: "Implement the mapping",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    epic.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	subtask, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Wire CLI flag",
		Description: "Add --jira",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    task.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	bug, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Handle empty repo",
		Description: "Schema requires issues",
		Type:        issues.TypeBug,
		Priority:    "urgent",
		ParentID:    epic.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddDependency(ctx, bug.ID, task.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	otherEpic, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Other epic",
		Description: "Should stay out",
		Type:        issues.TypeEpic,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	external, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Outside task",
		Description: "Do not export",
		Type:        issues.TypeTask,
		Priority:    "medium",
		ParentID:    otherEpic.ID,
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.AddDependency(ctx, external.ID, task.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}

	exported, err := s.ExportJira(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}

	if exported.ProjectKey != "" {
		t.Fatalf("expected empty project key, got %q", exported.ProjectKey)
	}

	if exported.Epic.IssueType != "Epic" || exported.Epic.Summary != epic.Title {
		t.Fatalf("unexpected epic export: %#v", exported.Epic)
	}

	if exported.Epic.Priority == nil || *exported.Epic.Priority != "High" {
		t.Fatalf("expected epic priority to map to High, got %#v", exported.Epic.Priority)
	}

	if exported.Options == nil || !exported.Options.CreateSubtasks {
		t.Fatalf("expected createSubtasks option, got %#v", exported.Options)
	}

	if len(exported.Issues) != 3 {
		t.Fatalf("expected three planned issues, got %#v", exported.Issues)
	}

	plannedByID := make(map[string]issues.JiraPlannedIssue, len(exported.Issues))
	for _, planned := range exported.Issues {
		plannedByID[planned.ClientID] = planned
	}

	if _, ok := plannedByID[external.ID]; ok {
		t.Fatalf("unexpected external issue in scoped export: %#v", plannedByID)
	}

	if plannedByID[task.ID].Issue.IssueType != "Task" || plannedByID[task.ID].ParentClientID != nil {
		t.Fatalf("expected task to stay a top-level task, got %#v", plannedByID[task.ID])
	}

	if plannedByID[subtask.ID].Issue.IssueType != "Sub-task" || plannedByID[subtask.ID].ParentClientID == nil || *plannedByID[subtask.ID].ParentClientID != task.ID {
		t.Fatalf("expected nested issue to become a sub-task, got %#v", plannedByID[subtask.ID])
	}

	if plannedByID[bug.ID].Issue.IssueType != "Bug" {
		t.Fatalf("expected bug type to map to Bug, got %#v", plannedByID[bug.ID])
	}

	if plannedByID[bug.ID].Issue.Priority == nil || *plannedByID[bug.ID].Issue.Priority != "Highest" {
		t.Fatalf("expected urgent priority to map to Highest, got %#v", plannedByID[bug.ID].Issue.Priority)
	}

	if len(exported.Dependencies) != 1 {
		t.Fatalf("expected one dependency link, got %#v", exported.Dependencies)
	}

	if exported.Dependencies[0].Type != "Blocks" || exported.Dependencies[0].InwardClientID != task.ID || exported.Dependencies[0].OutwardClientID != bug.ID {
		t.Fatalf("unexpected dependency export: %#v", exported.Dependencies[0])
	}
}

func TestExportJiraRejectsNonEpicIssue(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	task, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Not an epic",
		Description: "Plain task",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.ExportJira(ctx, task.ID)
	if err == nil || err.Error() != "issue "+task.ID+" is not an epic" {
		t.Fatalf("expected non-epic export to fail, got %v", err)
	}
}

func TestExportJiraRejectsEpicWithoutChildren(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	epic, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "Lonely epic",
		Description: "No children",
		Type:        issues.TypeEpic,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.ExportJira(ctx, epic.ID)
	if err == nil || err.Error() != "epic "+epic.ID+" has no child issues to export" {
		t.Fatalf("expected empty epic export to fail, got %v", err)
	}
}

func TestExportJiraRejectsUnknownIssue(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	_, err := s.ExportJira(ctx, "tk-99")
	if err == nil || err.Error() != "issue tk-99 not found" {
		t.Fatalf("expected unknown epic export to fail, got %v", err)
	}
}
