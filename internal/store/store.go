package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"tack/internal/issues"
)

const (
	schemaVersion     = "1"
	sqliteBusyTimeout = 5000
)

type Store struct {
	db   *sql.DB
	path string
}

type CreateIssueInput struct {
	Title       string
	Description string
	Type        string
	Priority    string
	ParentID    string
	DependsOn   []string
	Labels      []string
}

type ImportManifest struct {
	Issues []ImportIssueInput `json:"issues"`
}

type ImportIssueInput struct {
	DependsOn   []string `json:"depends_on"`
	Labels      []string `json:"labels"`
	Description string   `json:"description"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Priority    string   `json:"priority"`
	ID          string   `json:"id"`
	Parent      string   `json:"parent"`
}

type ImportResult struct {
	AliasMap   map[string]string `json:"alias_map"`
	CreatedIDs []string          `json:"created_ids"`
}

type UpdateIssueInput struct {
	Title       *string
	Description *string
	Type        *string
	Status      *string
	Priority    *string
	Assignee    *string
	ParentID    *string
	Claim       bool
}

type ListFilter struct {
	Statuses  []string
	Assignees []string
	Labels    []string
	Types     []string
	Limit     int
}

type FilterValueSource string

const (
	FilterValueSourceAll   FilterValueSource = "all"
	FilterValueSourceReady FilterValueSource = "ready"
)

type FilterValueKey string

const (
	FilterValueKeyStatus   FilterValueKey = "status"
	FilterValueKeyType     FilterValueKey = "type"
	FilterValueKeyLabel    FilterValueKey = "label"
	FilterValueKeyAssignee FilterValueKey = "assignee"
)

var (
	orderedFilterStatuses = []string{
		issues.StatusOpen,
		issues.StatusInProgress,
		issues.StatusBlocked,
		issues.StatusClosed,
	}
	orderedFilterTypes = []string{
		issues.TypeEpic,
		issues.TypeTask,
		issues.TypeBug,
		issues.TypeFeature,
	}
)

func validateReadyFilter(filter ListFilter) error {
	if len(filterAssignees(filter)) > 0 {
		return errors.New("ready queries do not support assignee filters")
	}

	return nil
}

func filterStatuses(filter ListFilter) []string {
	return normalizedFilterValues(filter.Statuses, nil)
}

func filterAssignees(filter ListFilter) []string {
	return normalizedFilterValues(filter.Assignees, nil)
}

func filterLabels(filter ListFilter) []string {
	return normalizedFilterValues(filter.Labels, strings.ToLower)
}

func filterTypes(filter ListFilter) []string {
	return normalizedFilterValues(filter.Types, nil)
}

func normalizedFilterValues(values []string, transform func(string) string) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))

	for _, value := range values {
		value = strings.TrimSpace(value)
		if transform != nil {
			value = transform(value)
		}

		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func listFilterClauses(filter ListFilter, includeAssignee bool) ([]string, []any) {
	var (
		clauses []string
		args    []any
	)

	clauses, args = appendAnyMatchClause(clauses, args, "i.status", filterStatuses(filter))
	if includeAssignee {
		clauses, args = appendAnyMatchClause(clauses, args, "COALESCE(i.assignee, '')", filterAssignees(filter))
	}

	clauses, args = appendAnyMatchClause(clauses, args, "i.type", filterTypes(filter))

	labels := filterLabels(filter)
	if len(labels) > 0 {
		clauses = append(clauses, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM issue_labels l WHERE l.issue_id = i.id AND l.label IN (%s))",
			sqlPlaceholders(len(labels)),
		))
		for _, label := range labels {
			args = append(args, label)
		}
	}

	return clauses, args
}

func appendAnyMatchClause(clauses []string, args []any, column string, values []string) ([]string, []any) {
	if len(values) == 0 {
		return clauses, args
	}

	clauses = append(clauses, fmt.Sprintf("%s IN (%s)", column, sqlPlaceholders(len(values))))
	for _, value := range values {
		args = append(args, value)
	}

	return clauses, args
}

func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}

	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func filterWithoutKey(filter ListFilter, key FilterValueKey) ListFilter {
	filter.Statuses = slices.Clone(filter.Statuses)
	filter.Assignees = slices.Clone(filter.Assignees)
	filter.Labels = slices.Clone(filter.Labels)
	filter.Types = slices.Clone(filter.Types)

	switch key {
	case FilterValueKeyStatus:
		filter.Statuses = nil
	case FilterValueKeyType:
		filter.Types = nil
	case FilterValueKeyLabel:
		filter.Labels = nil
	case FilterValueKeyAssignee:
		filter.Assignees = nil
	}

	return filter
}

func orderedPresentValues(order []string, present map[string]struct{}) []string {
	values := make([]string, 0, len(order))
	for _, value := range order {
		if _, ok := present[value]; ok {
			values = append(values, value)
		}
	}

	return values
}

func distinctSortedValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	unique := normalizedFilterValues(values, nil)
	slices.Sort(unique)

	return unique
}

func Open(path string) (*Store, error) {
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)

	s := &Store{db: db, path: path}

	err = s.migrate(context.Background())
	if err != nil {
		closeDB(db)

		return nil, err
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		fmt.Sprintf(`PRAGMA busy_timeout = %d;`, sqliteBusyTimeout),
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA journal_mode = WAL;`,
		`CREATE TABLE IF NOT EXISTS issues (
			id TEXT PRIMARY KEY,
			sequence INTEGER NOT NULL UNIQUE,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			priority TEXT NOT NULL,
			assignee TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			closed_at TEXT,
			parent_id TEXT,
			FOREIGN KEY(parent_id) REFERENCES issues(id) ON DELETE SET NULL
		);`,
		`CREATE TABLE IF NOT EXISTS issue_links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(source_id, target_id, kind),
			FOREIGN KEY(source_id) REFERENCES issues(id) ON DELETE CASCADE,
			FOREIGN KEY(target_id) REFERENCES issues(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS issue_labels (
			issue_id TEXT NOT NULL,
			label TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(issue_id, label),
			FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS issue_comments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			issue_id TEXT NOT NULL,
			body TEXT NOT NULL,
			author TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS issue_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			issue_id TEXT,
			actor TEXT NOT NULL,
			event_type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS issue_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`INSERT INTO issue_metadata(key, value) VALUES ('schema_version', '` + schemaVersion + `')
			ON CONFLICT(key) DO UPDATE SET value = excluded.value;`,
	}
	for _, stmt := range stmts {
		_, err := s.db.ExecContext(ctx, stmt)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) CreateIssue(ctx context.Context, input CreateIssueInput, actor string) (issues.Issue, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Priority = strings.ToLower(strings.TrimSpace(input.Priority))
	input.ParentID = strings.TrimSpace(input.ParentID)

	input.Labels = issues.NormalizeLabels(input.Labels)
	if input.Title == "" {
		return issues.Issue{}, errors.New("title is required")
	}

	if !issues.IsValidType(input.Type) {
		return issues.Issue{}, fmt.Errorf("invalid type %q", input.Type)
	}

	if !issues.IsValidPriority(input.Priority) {
		return issues.Issue{}, fmt.Errorf("invalid priority %q", input.Priority)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return issues.Issue{}, err
	}
	defer rollbackTx(tx)

	if input.ParentID != "" {
		err := ensureIssueExists(ctx, tx, input.ParentID)
		if err != nil {
			return issues.Issue{}, err
		}
	}

	for _, blocker := range input.DependsOn {
		err := ensureIssueExists(ctx, tx, blocker)
		if err != nil {
			return issues.Issue{}, err
		}
	}

	sequence, err := nextSequence(ctx, tx)
	if err != nil {
		return issues.Issue{}, err
	}

	id := fmt.Sprintf("tk-%d", sequence)
	now := time.Now().UTC()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO issues (
			id, sequence, title, description, type, status, priority, assignee,
			created_at, updated_at, closed_at, parent_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
	`, id, sequence, input.Title, input.Description, input.Type, issues.StatusOpen, input.Priority, "",
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), nullableString(input.ParentID))
	if err != nil {
		return issues.Issue{}, err
	}

	err = syncParentLink(ctx, tx, id, input.ParentID, now)
	if err != nil {
		return issues.Issue{}, err
	}

	for _, blocker := range input.DependsOn {
		err := addDependencyTx(ctx, tx, id, blocker, now)
		if err != nil {
			return issues.Issue{}, err
		}
	}

	err = addLabelsTx(ctx, tx, id, input.Labels, now)
	if err != nil {
		return issues.Issue{}, err
	}

	err = appendEventTx(ctx, tx, id, actor, "issue_created", map[string]any{
		"title":      input.Title,
		"type":       input.Type,
		"priority":   input.Priority,
		"parent_id":  input.ParentID,
		"depends_on": input.DependsOn,
		"labels":     input.Labels,
	})
	if err != nil {
		return issues.Issue{}, err
	}

	err = tx.Commit()
	if err != nil {
		return issues.Issue{}, err
	}

	return s.GetIssue(ctx, id)
}

func (s *Store) GetIssue(ctx context.Context, id string) (issues.Issue, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, sequence, title, description, type, status, priority, COALESCE(assignee, ''),
		       created_at, updated_at, closed_at, COALESCE(parent_id, '')
		FROM issues WHERE id = ?
	`, strings.TrimSpace(id))

	issue, err := scanIssue(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return issues.Issue{}, fmt.Errorf("issue %s not found", id)
		}

		return issues.Issue{}, err
	}

	labels, err := s.ListLabels(ctx, id)
	if err != nil {
		return issues.Issue{}, err
	}

	issue.Labels = labels

	return issue, nil
}

func (s *Store) GetIssueDetail(ctx context.Context, id string) (issues.IssueDetail, error) {
	issue, err := s.GetIssue(ctx, id)
	if err != nil {
		return issues.IssueDetail{}, err
	}

	detail := issues.IssueDetail{
		Issue:     issue,
		Comments:  []issues.Comment{},
		BlockedBy: []issues.Link{},
		Blocks:    []issues.Link{},
		Events:    []issues.Event{},
	}

	comments, err := s.listCommentsByIssueID(ctx, issue.ID)
	if err != nil {
		return issues.IssueDetail{}, err
	}

	deps, err := s.listDependenciesByIssueID(ctx, issue.ID)
	if err != nil {
		return issues.IssueDetail{}, err
	}

	events, err := s.listEventsByIssueID(ctx, issue.ID)
	if err != nil {
		return issues.IssueDetail{}, err
	}

	if comments != nil {
		detail.Comments = comments
	}

	if deps.BlockedBy != nil {
		detail.BlockedBy = deps.BlockedBy
	}

	if deps.Blocks != nil {
		detail.Blocks = deps.Blocks
	}

	if events != nil {
		detail.Events = events
	}

	return detail, nil
}

func (s *Store) IssueDetailView(ctx context.Context, id string) (issues.IssueDetailView, error) {
	issue, err := s.GetIssue(ctx, id)
	if err != nil {
		return issues.IssueDetailView{}, err
	}

	view := issues.IssueDetailView{
		Issue:    issue,
		Comments: []issues.Comment{},
		Events:   []issues.Event{},
		Dependencies: issues.DependencyList{
			IssueID:   issue.ID,
			BlockedBy: []issues.Link{},
			Blocks:    []issues.Link{},
		},
		RelatedSummaries: map[string]issues.IssueSummary{},
	}

	comments, err := s.listCommentsByIssueID(ctx, issue.ID)
	if err != nil {
		return issues.IssueDetailView{}, err
	}

	deps, err := s.listDependenciesByIssueID(ctx, issue.ID)
	if err != nil {
		return issues.IssueDetailView{}, err
	}

	events, err := s.listEventsByIssueID(ctx, issue.ID)
	if err != nil {
		return issues.IssueDetailView{}, err
	}

	if comments != nil {
		view.Comments = comments
	}

	if events != nil {
		view.Events = events
	}

	if deps.BlockedBy == nil {
		deps.BlockedBy = []issues.Link{}
	}

	if deps.Blocks == nil {
		deps.Blocks = []issues.Link{}
	}

	view.Dependencies = deps
	view.LatestCloseReason, view.LatestReopenReason = latestTransitionReasons(events)

	related, err := s.listIssueSummariesByID(ctx, relatedIssueIDs(issue, deps))
	if err != nil {
		return issues.IssueDetailView{}, err
	}

	for _, summary := range related {
		view.RelatedSummaries[summary.ID] = summary
	}

	return view, nil
}

func (s *Store) FocusedGraphView(ctx context.Context, id string) (issues.FocusedGraphView, error) {
	issue, err := s.GetIssue(ctx, id)
	if err != nil {
		return issues.FocusedGraphView{}, err
	}

	view := issues.FocusedGraphView{
		NodeSummaries: map[string]issues.IssueSummary{},
		SelectedID:    issue.ID,
		ParentID:      issue.ParentID,
		BlockedByIDs:  []string{},
		BlocksIDs:     []string{},
		ChildIDs:      []string{},
	}

	view.BlockedByIDs, err = s.listDirectBlockingIssueIDs(ctx, issue.ID)
	if err != nil {
		return issues.FocusedGraphView{}, err
	}

	view.BlocksIDs, err = s.listDirectBlockedIssueIDs(ctx, issue.ID)
	if err != nil {
		return issues.FocusedGraphView{}, err
	}

	view.ChildIDs, err = s.listDirectChildIssueIDs(ctx, issue.ID)
	if err != nil {
		return issues.FocusedGraphView{}, err
	}

	nodeIDs := []string{}
	seen := map[string]struct{}{}

	appendNodeID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}

		if _, ok := seen[id]; ok {
			return
		}

		seen[id] = struct{}{}
		nodeIDs = append(nodeIDs, id)
	}

	appendNodeID(issue.ID)
	appendNodeID(view.ParentID)

	for _, blockerID := range view.BlockedByIDs {
		appendNodeID(blockerID)
	}

	for _, blockedID := range view.BlocksIDs {
		appendNodeID(blockedID)
	}

	for _, childID := range view.ChildIDs {
		appendNodeID(childID)
	}

	summaries, err := s.listIssueSummariesByID(ctx, nodeIDs)
	if err != nil {
		return issues.FocusedGraphView{}, err
	}

	for _, summary := range summaries {
		view.NodeSummaries[summary.ID] = summary
	}

	return view, nil
}

func (s *Store) ProjectGraphView(ctx context.Context) (issues.ProjectGraphView, error) {
	summaries, err := s.ListIssueSummaries(ctx, ListFilter{})
	if err != nil {
		return issues.ProjectGraphView{}, err
	}

	links, err := s.listProjectGraphLinks(ctx)
	if err != nil {
		return issues.ProjectGraphView{}, err
	}

	if summaries == nil {
		summaries = []issues.IssueSummary{}
	}

	if links == nil {
		links = []issues.Link{}
	}

	return issues.ProjectGraphView{Issues: summaries, Links: links}, nil
}

func (s *Store) ListIssues(ctx context.Context, filter ListFilter) ([]issues.Issue, error) {
	query := `
		SELECT i.id, i.sequence, i.title, i.description, i.type, i.status, i.priority, COALESCE(i.assignee, ''),
		       i.created_at, i.updated_at, i.closed_at, COALESCE(i.parent_id, '')
		FROM issues i
	`

	var (
		where []string
		args  []any
	)

	where, args = listFilterClauses(filter, true)

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY i.sequence ASC"
	if filter.Limit > 0 {
		query += " LIMIT ?"

		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	return scanIssues(rows, s)
}

func (s *Store) ListIssueSummaries(ctx context.Context, filter ListFilter) ([]issues.IssueSummary, error) {
	query := `
		SELECT i.id, i.title, i.status, i.type, i.priority, COALESCE(i.assignee, ''), COALESCE(i.parent_id, '')
		FROM issues i
	`

	var (
		where []string
		args  []any
	)

	where, args = listFilterClauses(filter, true)

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY i.sequence ASC"
	if filter.Limit > 0 {
		query += " LIMIT ?"

		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	return scanIssueSummaries(ctx, rows, s)
}

func (s *Store) ListFilterValues(ctx context.Context, source FilterValueSource, key FilterValueKey, filter ListFilter) ([]string, error) {
	filter = filterWithoutKey(filter, key)

	var (
		summaries []issues.IssueSummary
		err       error
	)

	switch source {
	case FilterValueSourceAll:
		summaries, err = s.ListIssueSummaries(ctx, filter)
	case FilterValueSourceReady:
		summaries, err = s.ReadyIssueSummaries(ctx, filter)
	default:
		return nil, fmt.Errorf("unknown filter value source %q", source)
	}

	if err != nil {
		return nil, err
	}

	switch key {
	case FilterValueKeyStatus:
		present := make(map[string]struct{}, len(summaries))
		for _, summary := range summaries {
			present[summary.Status] = struct{}{}
		}

		return orderedPresentValues(orderedFilterStatuses, present), nil
	case FilterValueKeyType:
		present := make(map[string]struct{}, len(summaries))
		for _, summary := range summaries {
			present[summary.Type] = struct{}{}
		}

		return orderedPresentValues(orderedFilterTypes, present), nil
	case FilterValueKeyLabel:
		values := make([]string, 0)
		for _, summary := range summaries {
			values = append(values, summary.Labels...)
		}

		return distinctSortedValues(values), nil
	case FilterValueKeyAssignee:
		values := make([]string, 0, len(summaries))
		for _, summary := range summaries {
			if summary.Assignee == "" {
				continue
			}

			values = append(values, summary.Assignee)
		}

		return distinctSortedValues(values), nil
	default:
		return nil, fmt.Errorf("unknown filter value key %q", key)
	}
}

func (s *Store) ReadyIssues(ctx context.Context, filter ListFilter) ([]issues.Issue, error) {
	err := validateReadyFilter(filter)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT i.id, i.sequence, i.title, i.description, i.type, i.status, i.priority, COALESCE(i.assignee, ''),
		       i.created_at, i.updated_at, i.closed_at, COALESCE(i.parent_id, '')
		FROM issues i
		WHERE i.status = ?
		  AND COALESCE(i.assignee, '') = ''
		  AND NOT EXISTS (
			SELECT 1
			FROM issue_links l
			JOIN issues blocker ON blocker.id = l.source_id
			WHERE l.kind = 'blocks'
			  AND l.target_id = i.id
			  AND blocker.status != ?
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM issues child
			WHERE child.parent_id = i.id
			  AND child.status != ?
		  )
	`
	args := []any{
		issues.StatusOpen,
		issues.StatusClosed,
		issues.StatusClosed,
	}

	filterWhere, filterArgs := listFilterClauses(filter, false)
	if len(filterWhere) > 0 {
		query += " AND " + strings.Join(filterWhere, " AND ")

		args = append(args, filterArgs...)
	}

	query += " ORDER BY i.sequence ASC"
	if filter.Limit > 0 {
		query += " LIMIT ?"

		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	return scanIssues(rows, s)
}

func (s *Store) ReadyIssueSummaries(ctx context.Context, filter ListFilter) ([]issues.IssueSummary, error) {
	err := validateReadyFilter(filter)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT i.id, i.title, i.status, i.type, i.priority, COALESCE(i.assignee, ''), COALESCE(i.parent_id, '')
		FROM issues i
		WHERE i.status = ?
		  AND COALESCE(i.assignee, '') = ''
		  AND NOT EXISTS (
			SELECT 1
			FROM issue_links l
			JOIN issues blocker ON blocker.id = l.source_id
			WHERE l.kind = 'blocks'
			  AND l.target_id = i.id
			  AND blocker.status != ?
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM issues child
			WHERE child.parent_id = i.id
			  AND child.status != ?
		  )
	`
	args := []any{
		issues.StatusOpen,
		issues.StatusClosed,
		issues.StatusClosed,
	}

	filterWhere, filterArgs := listFilterClauses(filter, false)
	if len(filterWhere) > 0 {
		query += " AND " + strings.Join(filterWhere, " AND ")

		args = append(args, filterArgs...)
	}

	query += " ORDER BY i.sequence ASC"
	if filter.Limit > 0 {
		query += " LIMIT ?"

		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	return scanIssueSummaries(ctx, rows, s)
}

func (s *Store) UpdateIssue(ctx context.Context, id string, input UpdateIssueInput, actor string) (issues.Issue, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return issues.Issue{}, err
	}
	defer rollbackTx(tx)

	current, err := getIssueTx(ctx, tx, id)
	if err != nil {
		return issues.Issue{}, err
	}

	changed := map[string]any{}

	if input.Title != nil {
		v := strings.TrimSpace(*input.Title)
		if v == "" {
			return issues.Issue{}, errors.New("title cannot be empty")
		}

		current.Title = v
		changed["title"] = v
	}

	if input.Description != nil {
		current.Description = strings.TrimSpace(*input.Description)
		changed["description"] = current.Description
	}

	if input.Type != nil {
		v := strings.ToLower(strings.TrimSpace(*input.Type))
		if !issues.IsValidType(v) {
			return issues.Issue{}, fmt.Errorf("invalid type %q", v)
		}

		current.Type = v
		changed["type"] = v
	}

	if input.Status != nil {
		v := strings.ToLower(strings.TrimSpace(*input.Status))
		if !issues.IsValidStatus(v) {
			return issues.Issue{}, fmt.Errorf("invalid status %q", v)
		}

		current.Status = v
		if v == issues.StatusClosed {
			now := time.Now().UTC()
			current.ClosedAt = &now
		} else {
			current.ClosedAt = nil
		}

		changed["status"] = v
	}

	if input.Priority != nil {
		v := strings.ToLower(strings.TrimSpace(*input.Priority))
		if !issues.IsValidPriority(v) {
			return issues.Issue{}, fmt.Errorf("invalid priority %q", v)
		}

		current.Priority = v
		changed["priority"] = v
	}

	if input.Assignee != nil {
		current.Assignee = strings.TrimSpace(*input.Assignee)
		changed["assignee"] = current.Assignee
	}

	if input.ParentID != nil {
		parentID := strings.TrimSpace(*input.ParentID)
		if parentID == id {
			return issues.Issue{}, errors.New("issue cannot be its own parent")
		}

		if parentID != "" {
			err := ensureIssueExists(ctx, tx, parentID)
			if err != nil {
				return issues.Issue{}, err
			}

			err = validateParentAssignment(ctx, tx, id, parentID)
			if err != nil {
				return issues.Issue{}, err
			}
		}

		current.ParentID = parentID
		changed["parent_id"] = parentID
	}

	if input.Claim {
		if strings.TrimSpace(actor) == "" {
			return issues.Issue{}, errors.New("claim requires an actor")
		}

		if current.Status == issues.StatusClosed {
			return issues.Issue{}, errors.New("cannot claim closed issue")
		}

		if current.Assignee != "" && current.Assignee != actor {
			return issues.Issue{}, fmt.Errorf("issue %s is already claimed by %s", id, current.Assignee)
		}

		current.Assignee = actor
		if current.Status == issues.StatusOpen {
			current.Status = issues.StatusInProgress
		}

		changed["claim"] = true
		changed["assignee"] = current.Assignee
		changed["status"] = current.Status
	}

	if len(changed) == 0 {
		return issues.Issue{}, errors.New("no changes requested")
	}

	now := time.Now().UTC()

	_, err = tx.ExecContext(ctx, `
		UPDATE issues
		SET title = ?, description = ?, type = ?, status = ?, priority = ?, assignee = ?,
		    updated_at = ?, closed_at = ?, parent_id = ?
		WHERE id = ?
	`, current.Title, current.Description, current.Type, current.Status, current.Priority,
		nullableString(current.Assignee), now.Format(time.RFC3339Nano), nullableTime(current.ClosedAt),
		nullableString(current.ParentID), id)
	if err != nil {
		return issues.Issue{}, err
	}

	if input.ParentID != nil {
		err := syncParentLink(ctx, tx, id, current.ParentID, now)
		if err != nil {
			return issues.Issue{}, err
		}
	}

	err = appendEventTx(ctx, tx, id, actor, "issue_updated", changed)
	if err != nil {
		return issues.Issue{}, err
	}

	err = tx.Commit()
	if err != nil {
		return issues.Issue{}, err
	}

	return s.GetIssue(ctx, id)
}

func (s *Store) CloseIssue(ctx context.Context, id, reason, actor string) (issues.Issue, error) {
	return s.setClosedState(ctx, id, true, reason, actor)
}

func (s *Store) ReopenIssue(ctx context.Context, id, reason, actor string) (issues.Issue, error) {
	return s.setClosedState(ctx, id, false, reason, actor)
}

func (s *Store) setClosedState(ctx context.Context, id string, closed bool, reason, actor string) (issues.Issue, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return issues.Issue{}, err
	}
	defer rollbackTx(tx)

	issue, err := getIssueTx(ctx, tx, id)
	if err != nil {
		return issues.Issue{}, err
	}

	now := time.Now().UTC()

	if closed {
		issue.Status = issues.StatusClosed
		issue.ClosedAt = &now
	} else {
		issue.Status = issues.StatusOpen
		issue.ClosedAt = nil
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE issues SET status = ?, updated_at = ?, closed_at = ? WHERE id = ?
	`, issue.Status, now.Format(time.RFC3339Nano), nullableTime(issue.ClosedAt), id)
	if err != nil {
		return issues.Issue{}, err
	}

	eventType := "issue_reopened"
	if closed {
		eventType = "issue_closed"
	}

	err = appendEventTx(ctx, tx, id, actor, eventType, map[string]any{"reason": strings.TrimSpace(reason)})
	if err != nil {
		return issues.Issue{}, err
	}

	err = tx.Commit()
	if err != nil {
		return issues.Issue{}, err
	}

	return s.GetIssue(ctx, id)
}

func (s *Store) AddComment(ctx context.Context, id, body, actor string) (issues.Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return issues.Comment{}, errors.New("comment body is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return issues.Comment{}, err
	}
	defer rollbackTx(tx)

	err = ensureIssueExists(ctx, tx, id)
	if err != nil {
		return issues.Comment{}, err
	}

	now := time.Now().UTC()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO issue_comments(issue_id, body, author, created_at) VALUES (?, ?, ?, ?)
	`, id, body, actor, now.Format(time.RFC3339Nano))
	if err != nil {
		return issues.Comment{}, err
	}

	err = appendEventTx(ctx, tx, id, actor, "comment_added", map[string]any{"body": body})
	if err != nil {
		return issues.Comment{}, err
	}

	err = tx.Commit()
	if err != nil {
		return issues.Comment{}, err
	}

	commentID, err := result.LastInsertId()
	if err != nil {
		return issues.Comment{}, err
	}

	return issues.Comment{ID: commentID, IssueID: id, Body: body, Author: actor, CreatedAt: now}, nil
}

func (s *Store) ListComments(ctx context.Context, id string) ([]issues.Comment, error) {
	_, err := s.GetIssue(ctx, id)
	if err != nil {
		return nil, err
	}

	return s.listCommentsByIssueID(ctx, id)
}

func (s *Store) listCommentsByIssueID(ctx context.Context, id string) ([]issues.Comment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, issue_id, body, author, created_at
		FROM issue_comments
		WHERE issue_id = ?
		ORDER BY id ASC
	`, id)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	return scanComments(rows)
}

func (s *Store) AddDependency(ctx context.Context, blockedID, blockerID, actor string) (issues.Link, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return issues.Link{}, err
	}
	defer rollbackTx(tx)

	now := time.Now().UTC()

	link, inserted, err := addDependencyTxWithLink(ctx, tx, blockedID, blockerID, now)
	if err != nil {
		return issues.Link{}, err
	}

	if inserted {
		err = appendEventTx(ctx, tx, blockedID, actor, "dependency_added", map[string]any{
			"blocked_id": blockedID,
			"blocker_id": blockerID,
		})
		if err != nil {
			return issues.Link{}, err
		}
	}

	err = tx.Commit()
	if err != nil {
		return issues.Link{}, err
	}

	return link, nil
}

func (s *Store) RemoveDependency(ctx context.Context, blockedID, blockerID, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)

	err = ensureIssueExists(ctx, tx, blockedID)
	if err != nil {
		return err
	}

	err = ensureIssueExists(ctx, tx, blockerID)
	if err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, `
		DELETE FROM issue_links WHERE kind = 'blocks' AND source_id = ? AND target_id = ?
	`, blockerID, blockedID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected > 0 {
		err = appendEventTx(ctx, tx, blockedID, actor, "dependency_removed", map[string]any{
			"blocked_id": blockedID,
			"blocker_id": blockerID,
		})
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) ListDependencies(ctx context.Context, id string) (issues.DependencyList, error) {
	_, err := s.GetIssue(ctx, id)
	if err != nil {
		return issues.DependencyList{}, err
	}

	return s.listDependenciesByIssueID(ctx, id)
}

func (s *Store) listDependenciesByIssueID(ctx context.Context, id string) (issues.DependencyList, error) {
	blockedBy, err := s.listLinks(ctx, `SELECT id, source_id, target_id, kind, created_at FROM issue_links WHERE kind = 'blocks' AND target_id = ? ORDER BY id`, id)
	if err != nil {
		return issues.DependencyList{}, err
	}

	blocks, err := s.listLinks(ctx, `SELECT id, source_id, target_id, kind, created_at FROM issue_links WHERE kind = 'blocks' AND source_id = ? ORDER BY id`, id)
	if err != nil {
		return issues.DependencyList{}, err
	}

	return issues.DependencyList{IssueID: id, BlockedBy: blockedBy, Blocks: blocks}, nil
}

func (s *Store) listDirectBlockingIssueIDs(ctx context.Context, id string) ([]string, error) {
	return s.listIssueIDs(ctx, `
		SELECT blocker.id
		FROM issue_links l
		JOIN issues blocker ON blocker.id = l.source_id
		WHERE l.kind = 'blocks' AND l.target_id = ?
		ORDER BY blocker.sequence ASC
	`, id)
}

func (s *Store) listDirectBlockedIssueIDs(ctx context.Context, id string) ([]string, error) {
	return s.listIssueIDs(ctx, `
		SELECT blocked.id
		FROM issue_links l
		JOIN issues blocked ON blocked.id = l.target_id
		WHERE l.kind = 'blocks' AND l.source_id = ?
		ORDER BY blocked.sequence ASC
	`, id)
}

func (s *Store) listDirectChildIssueIDs(ctx context.Context, id string) ([]string, error) {
	return s.listIssueIDs(ctx, `
		SELECT child.id
		FROM issue_links l
		JOIN issues child ON child.id = l.target_id
		WHERE l.kind = 'parent_child' AND l.source_id = ?
		ORDER BY child.sequence ASC
	`, id)
}

func (s *Store) listProjectGraphLinks(ctx context.Context) ([]issues.Link, error) {
	return s.listLinks(ctx, `
		SELECT l.id, l.source_id, l.target_id, l.kind, l.created_at
		FROM issue_links l
		JOIN issues source ON source.id = l.source_id
		JOIN issues target ON target.id = l.target_id
		WHERE l.kind IN ('blocks', 'parent_child')
		ORDER BY
			CASE l.kind WHEN 'blocks' THEN 0 ELSE 1 END,
			source.sequence ASC,
			target.sequence ASC,
			l.id ASC
	`)
}

func (s *Store) listIssueSummariesByID(ctx context.Context, ids []string) ([]issues.IssueSummary, error) {
	if len(ids) == 0 {
		return []issues.IssueSummary{}, nil
	}

	query, args := queryForIDs(`
		SELECT i.id, i.title, i.status, i.type, i.priority, COALESCE(i.assignee, ''), COALESCE(i.parent_id, '')
		FROM issues i
		WHERE i.id IN (%s)
		ORDER BY i.sequence ASC
	`, ids)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	return scanIssueSummaries(ctx, rows, s)
}

func (s *Store) listEventsByIssueID(ctx context.Context, id string) ([]issues.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(issue_id, ''), actor, event_type, payload_json, created_at
		FROM issue_events
		WHERE issue_id = ?
		ORDER BY id ASC
	`, id)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	return scanEvents(rows)
}

func (s *Store) AddLabels(ctx context.Context, id string, labels []string, actor string) ([]string, error) {
	labels = issues.NormalizeLabels(labels)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackTx(tx)

	err = ensureIssueExists(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	err = addLabelsTx(ctx, tx, id, labels, now)
	if err != nil {
		return nil, err
	}

	err = appendEventTx(ctx, tx, id, actor, "labels_added", map[string]any{"labels": labels})
	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	return s.ListLabels(ctx, id)
}

func (s *Store) RemoveLabels(ctx context.Context, id string, labels []string, actor string) ([]string, error) {
	labels = issues.NormalizeLabels(labels)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackTx(tx)

	err = ensureIssueExists(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	for _, label := range labels {
		_, err = tx.ExecContext(ctx, `DELETE FROM issue_labels WHERE issue_id = ? AND label = ?`, id, label)
		if err != nil {
			return nil, err
		}
	}

	err = appendEventTx(ctx, tx, id, actor, "labels_removed", map[string]any{"labels": labels})
	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	return s.ListLabels(ctx, id)
}

func (s *Store) ReplaceLabels(ctx context.Context, id string, labels []string, actor string) ([]string, error) {
	labels = issues.NormalizeLabels(labels)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackTx(tx)

	err = ensureIssueExists(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM issue_labels WHERE issue_id = ?`, id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	err = addLabelsTx(ctx, tx, id, labels, now)
	if err != nil {
		return nil, err
	}

	err = appendEventTx(ctx, tx, id, actor, "labels_replaced", map[string]any{"labels": labels})
	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	return s.ListLabels(ctx, id)
}

func (s *Store) ListLabels(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT label FROM issue_labels WHERE issue_id = ? ORDER BY label ASC`, id)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var labels []string

	for rows.Next() {
		var label string

		err := rows.Scan(&label)
		if err != nil {
			return nil, err
		}

		labels = append(labels, label)
	}

	return labels, rows.Err()
}

func (s *Store) Export(ctx context.Context) (issues.Export, error) {
	allIssues, err := s.ListIssues(ctx, ListFilter{})
	if err != nil {
		return issues.Export{}, err
	}

	links, err := s.listLinks(ctx, `SELECT id, source_id, target_id, kind, created_at FROM issue_links ORDER BY id`)
	if err != nil {
		return issues.Export{}, err
	}

	commentsRows, err := s.db.QueryContext(ctx, `SELECT id, issue_id, body, author, created_at FROM issue_comments ORDER BY id`)
	if err != nil {
		return issues.Export{}, err
	}
	defer closeRows(commentsRows)

	comments, err := scanComments(commentsRows)
	if err != nil {
		return issues.Export{}, err
	}

	eventRows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(issue_id, ''), actor, event_type, payload_json, created_at FROM issue_events ORDER BY id`)
	if err != nil {
		return issues.Export{}, err
	}
	defer closeRows(eventRows)

	events, err := scanEvents(eventRows)
	if err != nil {
		return issues.Export{}, err
	}

	metaRows, err := s.db.QueryContext(ctx, `SELECT key, value FROM issue_metadata ORDER BY key`)
	if err != nil {
		return issues.Export{}, err
	}
	defer closeRows(metaRows)

	meta := make(map[string]any)

	for metaRows.Next() {
		var key, value string

		err := metaRows.Scan(&key, &value)
		if err != nil {
			return issues.Export{}, err
		}

		meta[key] = value
	}

	if originalPath, ok := meta["db_path"].(string); ok && strings.TrimSpace(originalPath) != "" && originalPath != s.path {
		meta["source_db_path"] = originalPath
	}

	meta["db_path"] = s.path

	return issues.Export{
		Issues:   allIssues,
		Links:    links,
		Comments: comments,
		Events:   events,
		Metadata: meta,
	}, nil
}

func (s *Store) ImportIssues(ctx context.Context, manifest ImportManifest, actor string) (ImportResult, error) {
	entries, err := normalizeImportManifest(manifest)
	if err != nil {
		return ImportResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer rollbackTx(tx)

	now := time.Now().UTC()
	createdIDs := make([]string, 0, len(entries))
	aliasMap := make(map[string]string, len(entries))

	for _, entry := range entries {
		sequence, err := nextSequence(ctx, tx)
		if err != nil {
			return ImportResult{}, err
		}

		id := fmt.Sprintf("tk-%d", sequence)

		_, err = tx.ExecContext(ctx, `
			INSERT INTO issues (
				id, sequence, title, description, type, status, priority, assignee,
				created_at, updated_at, closed_at, parent_id
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)
		`, id, sequence, entry.Title, entry.Description, entry.Type, issues.StatusOpen, entry.Priority, "",
			now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			return ImportResult{}, err
		}

		err = addLabelsTx(ctx, tx, id, entry.Labels, now)
		if err != nil {
			return ImportResult{}, err
		}

		aliasMap[entry.Alias] = id
		createdIDs = append(createdIDs, id)
	}

	for _, entry := range entries {
		id := aliasMap[entry.Alias]
		parentID := ""

		if entry.ParentAlias != "" {
			parentID = aliasMap[entry.ParentAlias]

			_, err = tx.ExecContext(ctx, `UPDATE issues SET parent_id = ? WHERE id = ?`, parentID, id)
			if err != nil {
				return ImportResult{}, err
			}

			err = syncParentLink(ctx, tx, id, parentID, now)
			if err != nil {
				return ImportResult{}, err
			}
		}

		blockerIDs := make([]string, 0, len(entry.DependsOn))

		for _, blockerAlias := range entry.DependsOn {
			blockerID := aliasMap[blockerAlias]
			blockerIDs = append(blockerIDs, blockerID)

			err = addDependencyTx(ctx, tx, id, blockerID, now)
			if err != nil {
				return ImportResult{}, err
			}
		}

		err = appendEventTx(ctx, tx, id, actor, "issue_created", map[string]any{
			"title":      entry.Title,
			"type":       entry.Type,
			"priority":   entry.Priority,
			"parent_id":  parentID,
			"depends_on": blockerIDs,
			"labels":     entry.Labels,
		})
		if err != nil {
			return ImportResult{}, err
		}
	}

	err = tx.Commit()
	if err != nil {
		return ImportResult{}, err
	}

	return ImportResult{AliasMap: aliasMap, CreatedIDs: createdIDs}, nil
}

func (s *Store) ImportSnapshot(ctx context.Context, data issues.Export) (ImportResult, error) {
	metadata, err := normalizeImportMetadata(data.Metadata)
	if err != nil {
		return ImportResult{}, err
	}

	err = validateImportSnapshot(data)
	if err != nil {
		return ImportResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer rollbackTx(tx)

	empty, err := isIssueStoreEmptyTx(ctx, tx)
	if err != nil {
		return ImportResult{}, err
	}

	if !empty {
		return ImportResult{}, errors.New("snapshot import requires an empty issue store")
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM issue_metadata`)
	if err != nil {
		return ImportResult{}, err
	}

	createdIDs := make([]string, 0, len(data.Issues))
	aliasMap := make(map[string]string, len(data.Issues))

	for _, issue := range data.Issues {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO issues (
				id, sequence, title, description, type, status, priority, assignee,
				created_at, updated_at, closed_at, parent_id
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		`, issue.ID, issue.Sequence, issue.Title, issue.Description, issue.Type, issue.Status, issue.Priority,
			nullableString(issue.Assignee), issue.CreatedAt.UTC().Format(time.RFC3339Nano),
			issue.UpdatedAt.UTC().Format(time.RFC3339Nano), nullableTime(issue.ClosedAt))
		if err != nil {
			return ImportResult{}, err
		}

		err = addLabelsTx(ctx, tx, issue.ID, issues.NormalizeLabels(issue.Labels), issue.CreatedAt.UTC())
		if err != nil {
			return ImportResult{}, err
		}

		createdIDs = append(createdIDs, issue.ID)
		aliasMap[issue.ID] = issue.ID
	}

	for _, issue := range data.Issues {
		parentID := strings.TrimSpace(issue.ParentID)
		if parentID == "" {
			continue
		}

		_, err = tx.ExecContext(ctx, `UPDATE issues SET parent_id = ? WHERE id = ?`, parentID, issue.ID)
		if err != nil {
			return ImportResult{}, err
		}
	}

	for _, link := range data.Links {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO issue_links(id, source_id, target_id, kind, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, link.ID, link.SourceID, link.TargetID, link.Kind, link.CreatedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return ImportResult{}, err
		}
	}

	for _, comment := range data.Comments {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO issue_comments(id, issue_id, body, author, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, comment.ID, comment.IssueID, comment.Body, comment.Author, comment.CreatedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return ImportResult{}, err
		}
	}

	for _, event := range data.Events {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO issue_events(id, issue_id, actor, event_type, payload_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, event.ID, nullableString(event.IssueID), event.Actor, event.EventType, event.Payload,
			event.CreatedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return ImportResult{}, err
		}
	}

	for _, key := range sortedMetadataKeys(metadata) {
		_, err = tx.ExecContext(ctx, `INSERT INTO issue_metadata(key, value) VALUES (?, ?)`, key, metadata[key])
		if err != nil {
			return ImportResult{}, err
		}
	}

	err = tx.Commit()
	if err != nil {
		return ImportResult{}, err
	}

	return ImportResult{AliasMap: aliasMap, CreatedIDs: createdIDs}, nil
}

func (s *Store) listLinks(ctx context.Context, query string, args ...any) ([]issues.Link, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var links []issues.Link

	for rows.Next() {
		var (
			link    issues.Link
			created string
		)

		err := rows.Scan(&link.ID, &link.SourceID, &link.TargetID, &link.Kind, &created)
		if err != nil {
			return nil, err
		}

		link.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}

		links = append(links, link)
	}

	return links, rows.Err()
}

func addDependencyTx(ctx context.Context, tx *sql.Tx, blockedID, blockerID string, now time.Time) error {
	_, _, err := addDependencyTxWithLink(ctx, tx, blockedID, blockerID, now)

	return err
}

func addDependencyTxWithLink(ctx context.Context, tx *sql.Tx, blockedID, blockerID string, now time.Time) (issues.Link, bool, error) {
	blockedID = strings.TrimSpace(blockedID)

	var err error

	blockerID = strings.TrimSpace(blockerID)
	if blockedID == blockerID {
		return issues.Link{}, false, errors.New("an issue cannot depend on itself")
	}

	err = ensureIssueExists(ctx, tx, blockedID)
	if err != nil {
		return issues.Link{}, false, err
	}

	err = ensureIssueExists(ctx, tx, blockerID)
	if err != nil {
		return issues.Link{}, false, err
	}

	hasCycle, err := dependencyPathExists(ctx, tx, blockedID, blockerID)
	if err != nil {
		return issues.Link{}, false, err
	}

	if hasCycle {
		return issues.Link{}, false, fmt.Errorf("dependency cycle detected between %s and %s", blockedID, blockerID)
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO issue_links(source_id, target_id, kind, created_at)
		VALUES (?, ?, 'blocks', ?)
		ON CONFLICT(source_id, target_id, kind) DO NOTHING
	`, blockerID, blockedID, now.Format(time.RFC3339Nano))
	if err != nil {
		return issues.Link{}, false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return issues.Link{}, false, err
	}

	if rowsAffected == 0 {
		link, err := getLinkTx(ctx, tx, blockerID, blockedID, "blocks")
		if err != nil {
			return issues.Link{}, false, err
		}

		return link, false, nil
	}

	id, err := result.LastInsertId()
	if err != nil {
		return issues.Link{}, false, err
	}

	return issues.Link{
		ID:        id,
		SourceID:  blockerID,
		TargetID:  blockedID,
		Kind:      "blocks",
		CreatedAt: now,
	}, true, nil
}

func dependencyPathExists(ctx context.Context, tx *sql.Tx, startBlocked, desiredBlocker string) (bool, error) {
	row := tx.QueryRowContext(ctx, `
		WITH RECURSIVE reach(source_id, target_id) AS (
			SELECT source_id, target_id
			FROM issue_links
			WHERE kind = 'blocks' AND source_id = ?
			UNION
			SELECT l.source_id, l.target_id
			FROM issue_links l
			JOIN reach r ON l.source_id = r.target_id
			WHERE l.kind = 'blocks'
		)
		SELECT EXISTS(SELECT 1 FROM reach WHERE target_id = ?)
	`, startBlocked, desiredBlocker)

	var exists bool

	err := row.Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

func validateParentAssignment(ctx context.Context, tx *sql.Tx, childID, parentID string) error {
	hasCycle, err := parentPathExists(ctx, tx, parentID, childID)
	if err != nil {
		return err
	}

	if hasCycle {
		return fmt.Errorf("parent cycle detected between %s and %s", childID, parentID)
	}

	return nil
}

func parentPathExists(ctx context.Context, tx *sql.Tx, startID, desiredAncestorID string) (bool, error) {
	row := tx.QueryRowContext(ctx, `
		WITH RECURSIVE ancestry(id, parent_id) AS (
			SELECT id, parent_id
			FROM issues
			WHERE id = ?
			UNION
			SELECT i.id, i.parent_id
			FROM issues i
			JOIN ancestry a ON i.id = a.parent_id
			WHERE a.parent_id IS NOT NULL
		)
		SELECT EXISTS(SELECT 1 FROM ancestry WHERE id = ?)
	`, startID, desiredAncestorID)

	var exists bool

	err := row.Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

func getLinkTx(ctx context.Context, tx *sql.Tx, sourceID, targetID, kind string) (issues.Link, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, source_id, target_id, kind, created_at
		FROM issue_links
		WHERE source_id = ? AND target_id = ? AND kind = ?
	`, sourceID, targetID, kind)

	var (
		link    issues.Link
		created string
	)

	err := row.Scan(&link.ID, &link.SourceID, &link.TargetID, &link.Kind, &created)
	if err != nil {
		return issues.Link{}, err
	}

	link.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return issues.Link{}, err
	}

	return link, nil
}

func addLabelsTx(ctx context.Context, tx *sql.Tx, id string, labels []string, now time.Time) error {
	for _, label := range labels {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO issue_labels(issue_id, label, created_at)
			VALUES (?, ?, ?)
			ON CONFLICT(issue_id, label) DO NOTHING
		`, id, label, now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
	}

	return nil
}

func syncParentLink(ctx context.Context, tx *sql.Tx, childID, parentID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM issue_links WHERE kind = 'parent_child' AND target_id = ?
	`, childID)
	if err != nil {
		return err
	}

	if parentID == "" {
		return nil
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO issue_links(source_id, target_id, kind, created_at)
		VALUES (?, ?, 'parent_child', ?)
	`, parentID, childID, now.Format(time.RFC3339Nano))

	return err
}

func appendEventTx(ctx context.Context, tx *sql.Tx, issueID, actor, eventType string, payload any) error {
	if strings.TrimSpace(actor) == "" {
		actor = "unknown"
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO issue_events(issue_id, actor, event_type, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, nullableString(issueID), actor, eventType, string(data), time.Now().UTC().Format(time.RFC3339Nano))

	return err
}

func ensureIssueExists(ctx context.Context, tx *sql.Tx, id string) error {
	var exists bool

	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM issues WHERE id = ?)`, strings.TrimSpace(id)).Scan(&exists)
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf("issue %s not found", id)
	}

	return nil
}

func nextSequence(ctx context.Context, tx *sql.Tx) (int64, error) {
	var next int64

	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM issues`).Scan(&next)
	if err != nil {
		return 0, err
	}

	return next, nil
}

func getIssueTx(ctx context.Context, tx *sql.Tx, id string) (issues.Issue, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, sequence, title, description, type, status, priority, COALESCE(assignee, ''),
		       created_at, updated_at, closed_at, COALESCE(parent_id, '')
		FROM issues WHERE id = ?
	`, id)

	issue, err := scanIssue(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return issues.Issue{}, fmt.Errorf("issue %s not found", id)
		}

		return issues.Issue{}, err
	}

	return issue, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanIssue(row scannable) (issues.Issue, error) {
	var (
		issue            issues.Issue
		created, updated string
		closed           sql.NullString
	)

	err := row.Scan(
		&issue.ID, &issue.Sequence, &issue.Title, &issue.Description, &issue.Type, &issue.Status,
		&issue.Priority, &issue.Assignee, &created, &updated, &closed, &issue.ParentID,
	)
	if err != nil {
		return issues.Issue{}, err
	}

	issue.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return issues.Issue{}, err
	}

	issue.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return issues.Issue{}, err
	}

	if closed.Valid {
		t, err := time.Parse(time.RFC3339Nano, closed.String)
		if err != nil {
			return issues.Issue{}, err
		}

		issue.ClosedAt = &t
	}

	return issue, nil
}

func scanIssues(rows *sql.Rows, s *Store) ([]issues.Issue, error) {
	var out []issues.Issue

	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, issue)
	}

	err := rows.Err()
	if err != nil {
		return nil, err
	}

	err = rows.Close()
	if err != nil {
		return nil, err
	}

	for i := range out {
		labels, err := s.ListLabels(context.Background(), out[i].ID)
		if err != nil {
			return nil, err
		}

		out[i].Labels = labels
	}

	return out, nil
}

func scanComments(rows *sql.Rows) ([]issues.Comment, error) {
	var comments []issues.Comment

	for rows.Next() {
		var (
			comment issues.Comment
			created string
		)

		err := rows.Scan(&comment.ID, &comment.IssueID, &comment.Body, &comment.Author, &created)
		if err != nil {
			return nil, err
		}

		comment.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}

		comments = append(comments, comment)
	}

	return comments, rows.Err()
}

func scanEvents(rows *sql.Rows) ([]issues.Event, error) {
	var events []issues.Event

	for rows.Next() {
		var (
			event   issues.Event
			created string
		)

		err := rows.Scan(&event.ID, &event.IssueID, &event.Actor, &event.EventType, &event.Payload, &created)
		if err != nil {
			return nil, err
		}

		event.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}

		events = append(events, event)
	}

	return events, rows.Err()
}

func (s *Store) listIssueIDs(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	ids := []string{}

	for rows.Next() {
		var id string

		err := rows.Scan(&id)
		if err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	return ids, rows.Err()
}

func scanIssueSummaries(ctx context.Context, rows *sql.Rows, s *Store) ([]issues.IssueSummary, error) {
	var (
		ids []string
		out []issues.IssueSummary
	)

	for rows.Next() {
		summary := issues.IssueSummary{
			Labels:       []string{},
			BlockedBy:    []string{},
			OpenChildren: []string{},
		}

		err := rows.Scan(
			&summary.ID,
			&summary.Title,
			&summary.Status,
			&summary.Type,
			&summary.Priority,
			&summary.Assignee,
			&summary.ParentID,
		)
		if err != nil {
			return nil, err
		}

		ids = append(ids, summary.ID)
		out = append(out, summary)
	}

	err := rows.Err()
	if err != nil {
		return nil, err
	}

	err = rows.Close()
	if err != nil {
		return nil, err
	}

	labelsByIssue, err := s.listSummaryLabels(ctx, ids)
	if err != nil {
		return nil, err
	}

	blockedByIssue, err := s.listSummaryBlockedBy(ctx, ids)
	if err != nil {
		return nil, err
	}

	openChildrenByIssue, err := s.listSummaryOpenChildren(ctx, ids)
	if err != nil {
		return nil, err
	}

	for i := range out {
		if labels, ok := labelsByIssue[out[i].ID]; ok {
			out[i].Labels = labels
		}

		if blockedBy, ok := blockedByIssue[out[i].ID]; ok {
			out[i].BlockedBy = blockedBy
		}

		if openChildren, ok := openChildrenByIssue[out[i].ID]; ok {
			out[i].OpenChildren = openChildren
		}
	}

	return out, nil
}

func (s *Store) listSummaryLabels(ctx context.Context, ids []string) (map[string][]string, error) {
	if len(ids) == 0 {
		return map[string][]string{}, nil
	}

	query, args := queryForIDs(`
		SELECT issue_id, label
		FROM issue_labels
		WHERE issue_id IN (%s)
		ORDER BY issue_id ASC, label ASC
	`, ids)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	labelsByIssue := make(map[string][]string, len(ids))

	for rows.Next() {
		var (
			issueID string
			label   string
		)

		err := rows.Scan(&issueID, &label)
		if err != nil {
			return nil, err
		}

		labelsByIssue[issueID] = append(labelsByIssue[issueID], label)
	}

	return labelsByIssue, rows.Err()
}

func (s *Store) listSummaryBlockedBy(ctx context.Context, ids []string) (map[string][]string, error) {
	if len(ids) == 0 {
		return map[string][]string{}, nil
	}

	query, args := queryForIDs(`
		SELECT l.target_id, blocker.id
		FROM issue_links l
		JOIN issues blocker ON blocker.id = l.source_id
		WHERE l.kind = 'blocks'
		  AND l.target_id IN (%s)
		  AND blocker.status != ?
		ORDER BY l.target_id ASC, blocker.sequence ASC
	`, ids)
	args = append(args, issues.StatusClosed)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	blockedByIssue := make(map[string][]string, len(ids))

	for rows.Next() {
		var issueID, blockerID string

		err := rows.Scan(&issueID, &blockerID)
		if err != nil {
			return nil, err
		}

		blockedByIssue[issueID] = append(blockedByIssue[issueID], blockerID)
	}

	return blockedByIssue, rows.Err()
}

func (s *Store) listSummaryOpenChildren(ctx context.Context, ids []string) (map[string][]string, error) {
	if len(ids) == 0 {
		return map[string][]string{}, nil
	}

	query, args := queryForIDs(`
		SELECT l.source_id, child.id
		FROM issue_links l
		JOIN issues child ON child.id = l.target_id
		WHERE l.kind = 'parent_child'
		  AND l.source_id IN (%s)
		  AND child.status != ?
		ORDER BY l.source_id ASC, child.sequence ASC
	`, ids)
	args = append(args, issues.StatusClosed)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	openChildrenByIssue := make(map[string][]string, len(ids))

	for rows.Next() {
		var issueID, childID string

		err := rows.Scan(&issueID, &childID)
		if err != nil {
			return nil, err
		}

		openChildrenByIssue[issueID] = append(openChildrenByIssue[issueID], childID)
	}

	return openChildrenByIssue, rows.Err()
}

func queryForIDs(query string, ids []string) (string, []any) {
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))

	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}

	return fmt.Sprintf(query, strings.Join(placeholders, ", ")), args
}

func relatedIssueIDs(issue issues.Issue, deps issues.DependencyList) []string {
	seen := map[string]struct{}{}
	ids := []string{}

	appendID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}

		if _, ok := seen[id]; ok {
			return
		}

		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	appendID(issue.ParentID)

	for _, link := range deps.BlockedBy {
		appendID(link.SourceID)
	}

	for _, link := range deps.Blocks {
		appendID(link.TargetID)
	}

	return ids
}

func latestTransitionReasons(events []issues.Event) (string, string) {
	var (
		closeReason  string
		reopenReason string
	)

	for i := len(events) - 1; i >= 0 && (closeReason == "" || reopenReason == ""); i-- {
		event := events[i]

		switch event.EventType {
		case "issue_closed":
			if closeReason == "" {
				closeReason = transitionReason(event.Payload)
			}
		case "issue_reopened":
			if reopenReason == "" {
				reopenReason = transitionReason(event.Payload)
			}
		}
	}

	return closeReason, reopenReason
}

func transitionReason(payload string) string {
	if strings.TrimSpace(payload) == "" {
		return ""
	}

	var data struct {
		Reason string `json:"reason"`
	}

	err := json.Unmarshal([]byte(payload), &data)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(data.Reason)
}

func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}

	return v
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}

	return t.UTC().Format(time.RFC3339Nano)
}

func closeDB(db *sql.DB) {
	err := db.Close()
	if err != nil {
		return
	}
}

func rollbackTx(tx *sql.Tx) {
	err := tx.Rollback()
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		return
	}
}

func closeRows(rows *sql.Rows) {
	err := rows.Close()
	if err != nil {
		return
	}
}

func isIssueStoreEmptyTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var count int

	err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM issues`).Scan(&count)
	if err != nil {
		return false, err
	}

	return count == 0, nil
}

func normalizeImportMetadata(metadata map[string]any) (map[string]string, error) {
	if len(metadata) == 0 {
		return map[string]string{}, nil
	}

	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, errors.New("metadata key cannot be empty")
		}

		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("metadata %q must be a string", key)
		}

		out[key] = text
	}

	return out, nil
}

func sortedMetadataKeys(metadata map[string]string) []string {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

type normalizedImportIssue struct {
	DependsOn   []string
	Labels      []string
	Alias       string
	Description string
	Title       string
	Type        string
	Priority    string
	ParentAlias string
}

func normalizeImportManifest(manifest ImportManifest) ([]normalizedImportIssue, error) {
	if len(manifest.Issues) == 0 {
		return nil, errors.New("manifest must contain at least one issue")
	}

	out := make([]normalizedImportIssue, 0, len(manifest.Issues))
	aliases := make(map[string]struct{}, len(manifest.Issues))

	for i, issue := range manifest.Issues {
		alias := strings.TrimSpace(issue.ID)
		if alias == "" {
			return nil, fmt.Errorf("issue %d: id is required", i+1)
		}

		if _, ok := aliases[alias]; ok {
			return nil, fmt.Errorf("duplicate manifest issue id %q", alias)
		}

		title := strings.TrimSpace(issue.Title)
		if title == "" {
			return nil, fmt.Errorf("issue %q: title is required", alias)
		}

		kind := strings.ToLower(strings.TrimSpace(issue.Type))
		if kind == "" {
			kind = issues.TypeTask
		}

		if !issues.IsValidType(kind) {
			return nil, fmt.Errorf("issue %q: invalid type %q", alias, kind)
		}

		priority := strings.ToLower(strings.TrimSpace(issue.Priority))
		if priority == "" {
			priority = "medium"
		}

		if !issues.IsValidPriority(priority) {
			return nil, fmt.Errorf("issue %q: invalid priority %q", alias, priority)
		}

		parentAlias := strings.TrimSpace(issue.Parent)
		if parentAlias == alias {
			return nil, fmt.Errorf("issue %q cannot be its own parent", alias)
		}

		dependsOn := normalizeImportAliases(issue.DependsOn)
		if slices.Contains(dependsOn, alias) {
			return nil, fmt.Errorf("issue %q cannot depend on itself", alias)
		}

		aliases[alias] = struct{}{}
		out = append(out, normalizedImportIssue{
			Alias:       alias,
			Title:       title,
			Description: strings.TrimSpace(issue.Description),
			Type:        kind,
			Priority:    priority,
			Labels:      issues.NormalizeLabels(issue.Labels),
			ParentAlias: parentAlias,
			DependsOn:   dependsOn,
		})
	}

	for _, issue := range out {
		if issue.ParentAlias != "" {
			if _, ok := aliases[issue.ParentAlias]; !ok {
				return nil, fmt.Errorf("issue %q references unknown parent %q", issue.Alias, issue.ParentAlias)
			}
		}

		for _, blockerAlias := range issue.DependsOn {
			if _, ok := aliases[blockerAlias]; !ok {
				return nil, fmt.Errorf("issue %q depends on unknown alias %q", issue.Alias, blockerAlias)
			}
		}
	}

	err := validateImportParentGraph(out)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func validateImportSnapshot(data issues.Export) error {
	if len(data.IssueData) > 0 {
		return errors.New("snapshot issue_data import is not supported")
	}

	issueIDs := make(map[string]struct{}, len(data.Issues))
	sequences := make(map[int64]struct{}, len(data.Issues))
	parentGraph := make([]normalizedImportIssue, 0, len(data.Issues))

	for i, issue := range data.Issues {
		id := strings.TrimSpace(issue.ID)
		if id == "" {
			return fmt.Errorf("snapshot issue %d: id is required", i+1)
		}

		if _, ok := issueIDs[id]; ok {
			return fmt.Errorf("duplicate snapshot issue id %q", id)
		}

		if issue.Sequence <= 0 {
			return fmt.Errorf("issue %q: sequence must be positive", id)
		}

		if _, ok := sequences[issue.Sequence]; ok {
			return fmt.Errorf("duplicate snapshot issue sequence %d", issue.Sequence)
		}

		if strings.TrimSpace(issue.Title) == "" {
			return fmt.Errorf("issue %q: title is required", id)
		}

		if !issues.IsValidType(issue.Type) {
			return fmt.Errorf("issue %q: invalid type %q", id, issue.Type)
		}

		if !issues.IsValidStatus(issue.Status) {
			return fmt.Errorf("issue %q: invalid status %q", id, issue.Status)
		}

		if !issues.IsValidPriority(issue.Priority) {
			return fmt.Errorf("issue %q: invalid priority %q", id, issue.Priority)
		}

		parentID := strings.TrimSpace(issue.ParentID)
		if parentID == id {
			return fmt.Errorf("issue %q cannot be its own parent", id)
		}

		issueIDs[id] = struct{}{}
		sequences[issue.Sequence] = struct{}{}

		parentGraph = append(parentGraph, normalizedImportIssue{
			Alias:       id,
			ParentAlias: parentID,
		})
	}

	for _, issue := range data.Issues {
		parentID := strings.TrimSpace(issue.ParentID)
		if parentID == "" {
			continue
		}

		if _, ok := issueIDs[parentID]; !ok {
			return fmt.Errorf("issue %q references unknown parent %q", issue.ID, parentID)
		}
	}

	err := validateImportParentGraph(parentGraph)
	if err != nil {
		return err
	}

	linkIDs := make(map[int64]struct{}, len(data.Links))
	for _, link := range data.Links {
		if _, ok := linkIDs[link.ID]; ok {
			return fmt.Errorf("duplicate snapshot link id %d", link.ID)
		}

		if _, ok := issueIDs[link.SourceID]; !ok {
			return fmt.Errorf("link %d references unknown source issue %q", link.ID, link.SourceID)
		}

		if _, ok := issueIDs[link.TargetID]; !ok {
			return fmt.Errorf("link %d references unknown target issue %q", link.ID, link.TargetID)
		}

		linkIDs[link.ID] = struct{}{}
	}

	commentIDs := make(map[int64]struct{}, len(data.Comments))
	for _, comment := range data.Comments {
		if _, ok := commentIDs[comment.ID]; ok {
			return fmt.Errorf("duplicate snapshot comment id %d", comment.ID)
		}

		if _, ok := issueIDs[comment.IssueID]; !ok {
			return fmt.Errorf("comment %d references unknown issue %q", comment.ID, comment.IssueID)
		}

		commentIDs[comment.ID] = struct{}{}
	}

	eventIDs := make(map[int64]struct{}, len(data.Events))
	for _, event := range data.Events {
		if _, ok := eventIDs[event.ID]; ok {
			return fmt.Errorf("duplicate snapshot event id %d", event.ID)
		}

		if issueID := strings.TrimSpace(event.IssueID); issueID != "" {
			if _, ok := issueIDs[issueID]; !ok {
				return fmt.Errorf("event %d references unknown issue %q", event.ID, issueID)
			}
		}

		if strings.TrimSpace(event.Payload) != "" && !json.Valid([]byte(event.Payload)) {
			return fmt.Errorf("event %d has invalid payload JSON", event.ID)
		}

		eventIDs[event.ID] = struct{}{}
	}

	return nil
}

func validateImportParentGraph(issues []normalizedImportIssue) error {
	parents := make(map[string]string, len(issues))
	states := make(map[string]int, len(issues))

	for _, issue := range issues {
		parents[issue.Alias] = issue.ParentAlias
	}

	var visit func(string) error

	visit = func(alias string) error {
		switch states[alias] {
		case 1:
			return fmt.Errorf("parent cycle detected involving %q", alias)
		case 2:
			return nil
		}

		states[alias] = 1

		if parentAlias := parents[alias]; parentAlias != "" {
			err := visit(parentAlias)
			if err != nil {
				return err
			}
		}

		states[alias] = 2

		return nil
	}

	for _, issue := range issues {
		err := visit(issue.Alias)
		if err != nil {
			return err
		}
	}

	return nil
}

func normalizeImportAliases(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}

		if _, ok := seen[trimmed]; ok {
			continue
		}

		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}

	return out
}
