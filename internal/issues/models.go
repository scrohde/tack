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
	ID              string     `json:"id"`
	Sequence        int64      `json:"sequence"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Type            string     `json:"type"`
	Status          string     `json:"status"`
	Priority        string     `json:"priority"`
	Assignee        string     `json:"assignee"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	ClosedAt        *time.Time `json:"closed_at"`
	ParentID        string     `json:"parent_id"`
	DeferredUntil   *time.Time `json:"deferred_until"`
	EstimateMinutes *int       `json:"estimate_minutes"`
	Labels          []string   `json:"labels"`
}

type Link struct {
	ID        int64     `json:"id"`
	SourceID  string    `json:"source_id"`
	TargetID  string    `json:"target_id"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
}

type DependencyList struct {
	IssueID   string `json:"issue_id"`
	BlockedBy []Link `json:"blocked_by"`
	Blocks    []Link `json:"blocks"`
	ParentOf  []Link `json:"parent_of,omitempty"`
	RelatedTo []Link `json:"related_to,omitempty"`
}

type Comment struct {
	ID        int64     `json:"id"`
	IssueID   string    `json:"issue_id"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

type Event struct {
	ID        int64     `json:"id"`
	IssueID   string    `json:"issue_id"`
	Actor     string    `json:"actor"`
	EventType string    `json:"event_type"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

type Export struct {
	Issues    []Issue          `json:"issues"`
	Links     []Link           `json:"links"`
	Comments  []Comment        `json:"comments"`
	Events    []Event          `json:"events"`
	Metadata  map[string]any   `json:"metadata"`
	IssueData map[string][]any `json:"issue_data,omitempty"`
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
