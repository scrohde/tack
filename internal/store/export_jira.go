package store

import (
	"context"
	"errors"
	"slices"
	"strings"

	"tack/internal/issues"
)

func (s *Store) ExportJira(ctx context.Context, epicID string) (issues.JiraEpicPlan, error) {
	data, err := s.Export(ctx)
	if err != nil {
		return issues.JiraEpicPlan{}, err
	}

	return buildJiraEpicPlan(data, epicID)
}

func buildJiraEpicPlan(data issues.Export, epicID string) (issues.JiraEpicPlan, error) {
	epicID = strings.TrimSpace(epicID)
	if epicID == "" {
		return issues.JiraEpicPlan{}, errors.New("jira export requires an epic id")
	}

	issueByID := make(map[string]issues.Issue, len(data.Issues))
	for _, issue := range data.Issues {
		issueByID[issue.ID] = issue
	}

	epic, ok := issueByID[epicID]
	if !ok {
		return issues.JiraEpicPlan{}, errors.New("issue " + epicID + " not found")
	}

	if epic.Type != issues.TypeEpic {
		return issues.JiraEpicPlan{}, errors.New("issue " + epicID + " is not an epic")
	}

	childrenByParent := make(map[string][]issues.Issue)

	for _, issue := range data.Issues {
		parentID := strings.TrimSpace(issue.ParentID)
		if parentID == "" {
			continue
		}

		childrenByParent[parentID] = append(childrenByParent[parentID], issue)
	}

	plannedSource := collectEpicDescendants(childrenByParent, epicID)
	if len(plannedSource) == 0 {
		return issues.JiraEpicPlan{}, errors.New("epic " + epicID + " has no child issues to export")
	}

	plan := issues.JiraEpicPlan{
		ProjectKey: "",
		Epic:       jiraIssueInput(epic, "Epic"),
		Issues:     make([]issues.JiraPlannedIssue, 0, len(plannedSource)),
	}

	hasSubtasks := false
	exportedIDs := make(map[string]struct{}, len(plannedSource))

	for _, issue := range plannedSource {
		parentClientID := jiraParentClientID(issue, epicID)
		if parentClientID != nil {
			hasSubtasks = true
		}

		plan.Issues = append(plan.Issues, issues.JiraPlannedIssue{
			ClientID:       issue.ID,
			Issue:          jiraIssueInput(issue, jiraPlannedIssueType(parentClientID, issue.Type)),
			ParentClientID: parentClientID,
		})
		exportedIDs[issue.ID] = struct{}{}
	}

	for _, link := range data.Links {
		if link.Kind != "blocks" {
			continue
		}

		if _, ok := exportedIDs[link.SourceID]; !ok {
			continue
		}

		if _, ok := exportedIDs[link.TargetID]; !ok {
			continue
		}

		plan.Dependencies = append(plan.Dependencies, issues.JiraDependencyLink{
			Type:            "Blocks",
			InwardClientID:  link.SourceID,
			OutwardClientID: link.TargetID,
		})
	}

	if hasSubtasks {
		plan.Options = &issues.JiraPlanOptions{CreateSubtasks: true}
	}

	return plan, nil
}

func collectEpicDescendants(childrenByParent map[string][]issues.Issue, epicID string) []issues.Issue {
	var out []issues.Issue

	var walk func(string)

	walk = func(parentID string) {
		for _, child := range childrenByParent[parentID] {
			out = append(out, child)
			walk(child.ID)
		}
	}

	walk(epicID)

	return out
}

func jiraIssueInput(issue issues.Issue, issueType string) issues.JiraIssueInput {
	input := issues.JiraIssueInput{
		IssueType: issueType,
		Summary:   issue.Title,
	}

	if description := strings.TrimSpace(issue.Description); description != "" {
		input.Description = description
	}

	if len(issue.Labels) > 0 {
		input.Labels = slices.Clone(issue.Labels)
	}

	if priority := jiraPriority(issue.Priority); priority != nil {
		input.Priority = priority
	}

	if assignee := optionalString(issue.Assignee); assignee != nil {
		input.Assignee = assignee
	}

	return input
}

func jiraPlannedIssueType(parentClientID *string, issueType string) string {
	if parentClientID != nil {
		return "Sub-task"
	}

	switch issueType {
	case issues.TypeBug:
		return "Bug"
	case issues.TypeFeature:
		return "Story"
	default:
		return "Task"
	}
}

func jiraParentClientID(issue issues.Issue, epicID string) *string {
	if issue.ParentID == "" || issue.ParentID == epicID {
		return nil
	}

	return optionalString(issue.ParentID)
}

func jiraPriority(priority string) *string {
	switch strings.TrimSpace(priority) {
	case "":
		return nil
	case "low":
		return optionalString("Low")
	case "medium":
		return optionalString("Medium")
	case "high":
		return optionalString("High")
	case "urgent":
		return optionalString("Highest")
	default:
		return optionalString(priority)
	}
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	return &value
}
