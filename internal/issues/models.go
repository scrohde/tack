package issues

import "time"

const (
	TypeEpic    = "epic"
	TypeTask    = "task"
	TypeBug     = "bug"
	TypeFeature = "feature"

	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusClosed     = "closed"
)

var (
	ValidTypes = map[string]struct{}{
		TypeEpic:    {},
		TypeTask:    {},
		TypeBug:     {},
		TypeFeature: {},
	}
	ValidStatuses = map[string]struct{}{
		StatusOpen:       {},
		StatusInProgress: {},
		StatusBlocked:    {},
		StatusClosed:     {},
	}
	ValidPriorities = map[string]struct{}{
		"low":    {},
		"medium": {},
		"high":   {},
		"urgent": {},
	}
)

type Issue struct {
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	EstimateMinutes *int       `json:"estimate_minutes"`
	DeferredUntil   *time.Time `json:"deferred_until"`
	ClosedAt        *time.Time `json:"closed_at"`
	Description     string     `json:"description"`
	Priority        string     `json:"priority"`
	Assignee        string     `json:"assignee"`
	Status          string     `json:"status"`
	Type            string     `json:"type"`
	ID              string     `json:"id"`
	ParentID        string     `json:"parent_id"`
	Title           string     `json:"title"`
	Labels          []string   `json:"labels"`
	Sequence        int64      `json:"sequence"`
}

type IssueSummary struct {
	Labels       []string `json:"labels"`
	BlockedBy    []string `json:"blocked_by"`
	OpenChildren []string `json:"open_children"`
	Priority     string   `json:"priority"`
	Assignee     string   `json:"assignee"`
	Status       string   `json:"status"`
	Type         string   `json:"type"`
	ID           string   `json:"id"`
	ParentID     string   `json:"parent_id"`
	Title        string   `json:"title"`
}

type Link struct {
	CreatedAt time.Time `json:"created_at"`
	SourceID  string    `json:"source_id"`
	TargetID  string    `json:"target_id"`
	Kind      string    `json:"kind"`
	ID        int64     `json:"id"`
}

type DependencyList struct {
	IssueID   string `json:"issue_id"`
	BlockedBy []Link `json:"blocked_by"`
	Blocks    []Link `json:"blocks"`
	ParentOf  []Link `json:"parent_of,omitempty"`
	RelatedTo []Link `json:"related_to,omitempty"`
}

type Comment struct {
	CreatedAt time.Time `json:"created_at"`
	IssueID   string    `json:"issue_id"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	ID        int64     `json:"id"`
}

type Event struct {
	CreatedAt time.Time `json:"created_at"`
	IssueID   string    `json:"issue_id"`
	Actor     string    `json:"actor"`
	EventType string    `json:"event_type"`
	Payload   string    `json:"payload"`
	ID        int64     `json:"id"`
}

type Export struct {
	Metadata  map[string]any   `json:"metadata"`
	IssueData map[string][]any `json:"issue_data,omitempty"`
	Issues    []Issue          `json:"issues"`
	Links     []Link           `json:"links"`
	Comments  []Comment        `json:"comments"`
	Events    []Event          `json:"events"`
}

func IsValidType(v string) bool {
	_, ok := ValidTypes[v]

	return ok
}

func IsValidStatus(v string) bool {
	_, ok := ValidStatuses[v]

	return ok
}

func IsValidPriority(v string) bool {
	_, ok := ValidPriorities[v]

	return ok
}
