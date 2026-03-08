package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"tack/internal/issues"
)

const schemaVersion = "1"

type Store struct {
	db   *sql.DB
	path string
}

type CreateIssueInput struct {
	DeferredUntil   *time.Time
	EstimateMinutes *int
	Title           string
	Description     string
	Type            string
	Priority        string
	ParentID        string
	DependsOn       []string
	Labels          []string
}

type UpdateIssueInput struct {
	Title              *string
	Description        *string
	Type               *string
	Status             *string
	Priority           *string
	Assignee           *string
	ParentID           *string
	DeferredUntil      *time.Time
	EstimateMinutes    *int
	HasDeferredUntil   bool
	HasEstimateMinutes bool
	Claim              bool
}

type ListFilter struct {
	Status   string
	Assignee string
	Label    string
	Type     string
	Limit    int
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
		`PRAGMA foreign_keys = ON;`,
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
			deferred_until TEXT,
			estimate_minutes INTEGER,
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
			created_at, updated_at, closed_at, parent_id, deferred_until, estimate_minutes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)
	`, id, sequence, input.Title, input.Description, input.Type, issues.StatusOpen, input.Priority, "",
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), nullableString(input.ParentID),
		nullableTime(input.DeferredUntil), nullableInt(input.EstimateMinutes))
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
		       created_at, updated_at, closed_at, COALESCE(parent_id, ''), deferred_until, estimate_minutes
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

func (s *Store) ListIssues(ctx context.Context, filter ListFilter) ([]issues.Issue, error) {
	query := `
		SELECT i.id, i.sequence, i.title, i.description, i.type, i.status, i.priority, COALESCE(i.assignee, ''),
		       i.created_at, i.updated_at, i.closed_at, COALESCE(i.parent_id, ''), i.deferred_until, i.estimate_minutes
		FROM issues i
	`

	var (
		where []string
		args  []any
	)

	if filter.Status != "" {
		where = append(where, "i.status = ?")
		args = append(args, filter.Status)
	}

	if filter.Assignee != "" {
		where = append(where, "COALESCE(i.assignee, '') = ?")
		args = append(args, filter.Assignee)
	}

	if filter.Type != "" {
		where = append(where, "i.type = ?")
		args = append(args, filter.Type)
	}

	if filter.Label != "" {
		where = append(where, "EXISTS (SELECT 1 FROM issue_labels l WHERE l.issue_id = i.id AND l.label = ?)")
		args = append(args, strings.ToLower(filter.Label))
	}

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

func (s *Store) ReadyIssues(ctx context.Context, filter ListFilter) ([]issues.Issue, error) {
	query := `
		SELECT i.id, i.sequence, i.title, i.description, i.type, i.status, i.priority, COALESCE(i.assignee, ''),
		       i.created_at, i.updated_at, i.closed_at, COALESCE(i.parent_id, ''), i.deferred_until, i.estimate_minutes
		FROM issues i
		WHERE i.status = ?
		  AND COALESCE(i.assignee, '') = ''
		  AND (i.deferred_until IS NULL OR i.deferred_until <= ?)
		  AND NOT EXISTS (
			SELECT 1
			FROM issue_links l
			JOIN issues blocker ON blocker.id = l.source_id
			WHERE l.kind = 'blocks'
			  AND l.target_id = i.id
			  AND blocker.status != ?
		  )
	`
	args := []any{issues.StatusOpen, time.Now().UTC().Format(time.RFC3339Nano), issues.StatusClosed}

	if filter.Assignee != "" {
		query += " AND COALESCE(i.assignee, '') = ?"

		args = append(args, filter.Assignee)
	}

	if filter.Type != "" {
		query += " AND i.type = ?"

		args = append(args, filter.Type)
	}

	if filter.Label != "" {
		query += " AND EXISTS (SELECT 1 FROM issue_labels l WHERE l.issue_id = i.id AND l.label = ?)"

		args = append(args, strings.ToLower(filter.Label))
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
		}

		current.ParentID = parentID
		changed["parent_id"] = parentID
	}

	if input.HasDeferredUntil {
		current.DeferredUntil = input.DeferredUntil
		changed["deferred_until"] = input.DeferredUntil
	}

	if input.HasEstimateMinutes {
		current.EstimateMinutes = input.EstimateMinutes
		changed["estimate_minutes"] = input.EstimateMinutes
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
		    updated_at = ?, closed_at = ?, parent_id = ?, deferred_until = ?, estimate_minutes = ?
		WHERE id = ?
	`, current.Title, current.Description, current.Type, current.Status, current.Priority,
		nullableString(current.Assignee), now.Format(time.RFC3339Nano), nullableTime(current.ClosedAt),
		nullableString(current.ParentID), nullableTime(current.DeferredUntil), nullableInt(current.EstimateMinutes), id)
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

	var comments []issues.Comment

	for rows.Next() {
		var (
			c       issues.Comment
			created string
		)

		err := rows.Scan(&c.ID, &c.IssueID, &c.Body, &c.Author, &created)
		if err != nil {
			return nil, err
		}

		c.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}

		comments = append(comments, c)
	}

	return comments, rows.Err()
}

func (s *Store) AddDependency(ctx context.Context, blockedID, blockerID, actor string) (issues.Link, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return issues.Link{}, err
	}
	defer rollbackTx(tx)

	now := time.Now().UTC()

	link, err := addDependencyTxWithLink(ctx, tx, blockedID, blockerID, now)
	if err != nil {
		return issues.Link{}, err
	}

	err = appendEventTx(ctx, tx, blockedID, actor, "dependency_added", map[string]any{
		"blocked_id": blockedID,
		"blocker_id": blockerID,
	})
	if err != nil {
		return issues.Link{}, err
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

	_, err = tx.ExecContext(ctx, `
		DELETE FROM issue_links WHERE kind = 'blocks' AND source_id = ? AND target_id = ?
	`, blockerID, blockedID)
	if err != nil {
		return err
	}

	err = appendEventTx(ctx, tx, blockedID, actor, "dependency_removed", map[string]any{
		"blocked_id": blockedID,
		"blocker_id": blockerID,
	})
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) ListDependencies(ctx context.Context, id string) (issues.DependencyList, error) {
	_, err := s.GetIssue(ctx, id)
	if err != nil {
		return issues.DependencyList{}, err
	}

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

	var comments []issues.Comment

	for commentsRows.Next() {
		var (
			c       issues.Comment
			created string
		)

		err := commentsRows.Scan(&c.ID, &c.IssueID, &c.Body, &c.Author, &created)
		if err != nil {
			return issues.Export{}, err
		}

		c.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return issues.Export{}, err
		}

		comments = append(comments, c)
	}

	eventRows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(issue_id, ''), actor, event_type, payload_json, created_at FROM issue_events ORDER BY id`)
	if err != nil {
		return issues.Export{}, err
	}
	defer closeRows(eventRows)

	var events []issues.Event

	for eventRows.Next() {
		var (
			e       issues.Event
			created string
		)

		err := eventRows.Scan(&e.ID, &e.IssueID, &e.Actor, &e.EventType, &e.Payload, &created)
		if err != nil {
			return issues.Export{}, err
		}

		e.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return issues.Export{}, err
		}

		events = append(events, e)
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

	meta["db_path"] = s.path

	return issues.Export{
		Issues:   allIssues,
		Links:    links,
		Comments: comments,
		Events:   events,
		Metadata: meta,
	}, nil
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
	_, err := addDependencyTxWithLink(ctx, tx, blockedID, blockerID, now)

	return err
}

func addDependencyTxWithLink(ctx context.Context, tx *sql.Tx, blockedID, blockerID string, now time.Time) (issues.Link, error) {
	blockedID = strings.TrimSpace(blockedID)

	var err error

	blockerID = strings.TrimSpace(blockerID)
	if blockedID == blockerID {
		return issues.Link{}, errors.New("an issue cannot depend on itself")
	}

	err = ensureIssueExists(ctx, tx, blockedID)
	if err != nil {
		return issues.Link{}, err
	}

	err = ensureIssueExists(ctx, tx, blockerID)
	if err != nil {
		return issues.Link{}, err
	}

	hasCycle, err := dependencyPathExists(ctx, tx, blockedID, blockerID)
	if err != nil {
		return issues.Link{}, err
	}

	if hasCycle {
		return issues.Link{}, fmt.Errorf("dependency cycle detected between %s and %s", blockedID, blockerID)
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO issue_links(source_id, target_id, kind, created_at)
		VALUES (?, ?, 'blocks', ?)
		ON CONFLICT(source_id, target_id, kind) DO NOTHING
	`, blockerID, blockedID, now.Format(time.RFC3339Nano))
	if err != nil {
		return issues.Link{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return issues.Link{}, err
	}

	return issues.Link{
		ID:        id,
		SourceID:  blockerID,
		TargetID:  blockedID,
		Kind:      "blocks",
		CreatedAt: now,
	}, nil
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
		       created_at, updated_at, closed_at, COALESCE(parent_id, ''), deferred_until, estimate_minutes
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
		deferred         sql.NullString
		estimate         sql.NullInt64
	)

	err := row.Scan(
		&issue.ID, &issue.Sequence, &issue.Title, &issue.Description, &issue.Type, &issue.Status,
		&issue.Priority, &issue.Assignee, &created, &updated, &closed, &issue.ParentID, &deferred, &estimate,
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

	if deferred.Valid {
		t, err := time.Parse(time.RFC3339Nano, deferred.String)
		if err != nil {
			return issues.Issue{}, err
		}

		issue.DeferredUntil = &t
	}

	if estimate.Valid {
		v := int(estimate.Int64)
		issue.EstimateMinutes = &v
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

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}

	return *v
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
