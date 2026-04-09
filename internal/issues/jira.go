package issues

type JiraEpicPlan struct {
	ProjectKey   string               `json:"projectKey"`
	Epic         JiraIssueInput       `json:"epic"`
	Issues       []JiraPlannedIssue   `json:"issues"`
	Dependencies []JiraDependencyLink `json:"dependencies,omitempty"`
	Options      *JiraPlanOptions     `json:"options,omitempty"`
}

type JiraPlanOptions struct {
	DefaultAssignee *string `json:"defaultAssignee,omitempty"`
	DefaultPriority *string `json:"defaultPriority,omitempty"`
	CreateSubtasks  bool    `json:"createSubtasks"`
}

type JiraIssueInput struct {
	IssueType    string         `json:"issueType"`
	Summary      string         `json:"summary"`
	Description  any            `json:"description,omitempty"`
	Labels       []string       `json:"labels,omitempty"`
	Components   []string       `json:"components,omitempty"`
	Priority     *string        `json:"priority,omitempty"`
	Assignee     *string        `json:"assignee,omitempty"`
	Reporter     *string        `json:"reporter,omitempty"`
	DueDate      *string        `json:"dueDate,omitempty"`
	StoryPoints  *float64       `json:"storyPoints,omitempty"`
	CustomFields map[string]any `json:"customFields,omitempty"`
}

type JiraPlannedIssue struct {
	ClientID          string         `json:"clientId"`
	Issue             JiraIssueInput `json:"issue"`
	ParentClientID    *string        `json:"parentClientId,omitempty"`
	RankAfterClientID *string        `json:"rankAfterClientId,omitempty"`
}

type JiraDependencyLink struct {
	Type            string  `json:"type"`
	InwardClientID  string  `json:"inwardClientId"`
	OutwardClientID string  `json:"outwardClientId"`
	Comment         *string `json:"comment,omitempty"`
}
