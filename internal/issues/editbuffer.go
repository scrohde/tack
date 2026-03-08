package issues

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type EditableIssue struct {
	Title           string
	Description     string
	Type            string
	Status          string
	Priority        string
	Assignee        string
	ParentID        string
	DeferredUntil   *time.Time
	EstimateMinutes *int
	Labels          []string
}

func FormatEditableIssue(issue Issue) string {
	deferred := ""
	if issue.DeferredUntil != nil {
		deferred = issue.DeferredUntil.Format(time.RFC3339)
	}

	estimate := ""
	if issue.EstimateMinutes != nil {
		estimate = strconv.Itoa(*issue.EstimateMinutes)
	}

	return fmt.Sprintf(`# Edit mutable tack issue fields. Empty values clear optional fields.
title: %s
type: %s
status: %s
priority: %s
assignee: %s
parent: %s
deferred_until: %s
estimate_minutes: %s
labels: %s
description:
<<<
%s
>>>
`, issue.Title, issue.Type, issue.Status, issue.Priority, issue.Assignee, issue.ParentID, deferred, estimate, strings.Join(issue.Labels, ","), issue.Description)
}

func ParseEditableIssue(body string) (EditableIssue, error) {
	lines := strings.Split(body, "\n")

	var out EditableIssue

	inDescription := false

	var description []string

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") && !inDescription {
			continue
		}

		if trimmed == "<<<" {
			inDescription = true

			continue
		}

		if trimmed == ">>>" {
			inDescription = false

			continue
		}

		if inDescription {
			description = append(description, line)

			continue
		}

		if trimmed == "" || trimmed == "description:" {
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return out, fmt.Errorf("invalid line in edit buffer: %q", line)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "title":
			out.Title = value
		case "type":
			out.Type = value
		case "status":
			out.Status = value
		case "priority":
			out.Priority = value
		case "assignee":
			out.Assignee = value
		case "parent":
			out.ParentID = value
		case "deferred_until":
			if value != "" {
				t, err := time.Parse(time.RFC3339, value)
				if err != nil {
					return out, fmt.Errorf("invalid deferred_until: %w", err)
				}

				out.DeferredUntil = &t
			}
		case "estimate_minutes":
			if value != "" {
				n, err := strconv.Atoi(value)
				if err != nil {
					return out, fmt.Errorf("invalid estimate_minutes: %w", err)
				}

				out.EstimateMinutes = &n
			}
		case "labels":
			out.Labels = NormalizeLabels(strings.Split(value, ","))
		default:
			return out, fmt.Errorf("unknown edit field: %s", key)
		}
	}

	out.Description = strings.Trim(strings.Join(description, "\n"), "\n")

	return out, nil
}

func NormalizeLabels(labels []string) []string {
	seen := make(map[string]struct{}, len(labels))

	var out []string

	for _, label := range labels {
		v := strings.ToLower(strings.TrimSpace(label))
		if v == "" {
			continue
		}

		if _, ok := seen[v]; ok {
			continue
		}

		seen[v] = struct{}{}
		out = append(out, v)
	}

	return out
}
