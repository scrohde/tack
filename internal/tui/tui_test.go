package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"tack/internal/issues"
	"tack/internal/store"
	"tack/internal/testutil"
)

func TestNewModelLoadsSummariesFromStartupSource(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "all issue", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		readySummaries: []issues.IssueSummary{
			{ID: "tk-2", Title: "ready issue", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-2": {Issue: issues.Issue{ID: "tk-2", Title: "ready issue", Status: issues.StatusOpen}},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-2": {SelectedID: "tk-2", NodeSummaries: map[string]issues.IssueSummary{"tk-2": {ID: "tk-2", Title: "ready issue", Status: issues.StatusOpen}}},
		},
		project: issues.ProjectGraphView{},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{Source: DataSourceReady})
	if err != nil {
		t.Fatal(err)
	}

	if reader.readyCalls != 1 || reader.listCalls != 0 {
		t.Fatalf("unexpected startup calls: %#v", reader)
	}

	if len(m.summaries) != 1 || m.summaries[0].ID != "tk-2" {
		t.Fatalf("unexpected summaries: %#v", m.summaries)
	}

	if got := m.currentDetailID(); got != "tk-2" {
		t.Fatalf("unexpected detail id: %s", got)
	}

	view := m.View()
	if !view.AltScreen {
		t.Fatalf("expected alt screen view: %#v", view)
	}
}

func TestSelectionChangesRefreshActiveDetailWhenUnpinned(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "first", Status: issues.StatusOpen, Type: issues.TypeTask},
			{ID: "tk-2", Title: "second", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "first", Status: issues.StatusOpen}},
			"tk-2": {Issue: issues.Issue{ID: "tk-2", Title: "second", Status: issues.StatusOpen}},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-1": {SelectedID: "tk-1", NodeSummaries: map[string]issues.IssueSummary{"tk-1": {ID: "tk-1", Title: "first", Status: issues.StatusOpen}}},
			"tk-2": {SelectedID: "tk-2", NodeSummaries: map[string]issues.IssueSummary{"tk-2": {ID: "tk-2", Title: "second", Status: issues.StatusOpen}}},
		},
		project: issues.ProjectGraphView{},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if m.detailView.Issue.ID != "tk-1" {
		t.Fatalf("unexpected initial detail: %#v", m.detailView)
	}

	m.handleKey("j")

	if m.selected != 1 {
		t.Fatalf("expected selection to move, got %d", m.selected)
	}

	if m.detailView.Issue.ID != "tk-2" {
		t.Fatalf("expected detail to follow selection, got %#v", m.detailView)
	}

	if m.focusedGraphView.SelectedID != "tk-2" {
		t.Fatalf("expected focused graph to follow selection, got %#v", m.focusedGraphView)
	}
}

func TestTabNavigationAndEscapeAreDeterministic(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "first", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "first", Status: issues.StatusOpen}},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-1": {SelectedID: "tk-1", NodeSummaries: map[string]issues.IssueSummary{"tk-1": {ID: "tk-1", Title: "first", Status: issues.StatusOpen}}},
		},
		project: issues.ProjectGraphView{},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if m.focus != paneBrowser || m.activeTab != tabDetails {
		t.Fatalf("unexpected initial focus state: focus=%s tab=%d", m.focus, m.activeTab)
	}

	m.handleKey("tab")

	if m.focus != paneDetail || m.activeTab != tabDetails {
		t.Fatalf("expected detail pane focus, got focus=%s tab=%d", m.focus, m.activeTab)
	}

	m.handleKey("tab")

	if m.activeTab != tabComments {
		t.Fatalf("expected comments tab, got %d", m.activeTab)
	}

	m.handleKey("tab")

	if m.activeTab != tabFocusedGraph {
		t.Fatalf("expected focused graph tab, got %d", m.activeTab)
	}

	m.handleKey("shift+tab")

	if m.activeTab != tabComments {
		t.Fatalf("expected comments tab after reverse, got %d", m.activeTab)
	}

	m.handleKey("enter")

	if m.pinnedID != "tk-1" || m.focus != paneDetail {
		t.Fatalf("expected pinned detail, got pinned=%q focus=%s", m.pinnedID, m.focus)
	}

	m.handleKey("esc")

	if m.focus != paneBrowser || m.pinnedID != "" {
		t.Fatalf("expected browser focus and cleared pin, got pinned=%q focus=%s", m.pinnedID, m.focus)
	}
}

func TestFilterEditorUpdatesFilterAndSummaries(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		listByFilter: func(filter store.ListFilter) []issues.IssueSummary {
			switch filter.Status {
			case "blocked":
				return []issues.IssueSummary{
					{ID: "tk-2", Title: "blocked issue", Status: issues.StatusBlocked, Type: issues.TypeBug},
				}
			default:
				return []issues.IssueSummary{
					{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen, Type: issues.TypeTask},
					{ID: "tk-2", Title: "blocked issue", Status: issues.StatusBlocked, Type: issues.TypeBug},
				}
			}
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen}},
			"tk-2": {Issue: issues.Issue{ID: "tk-2", Title: "blocked issue", Status: issues.StatusBlocked}},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-1": {SelectedID: "tk-1", NodeSummaries: map[string]issues.IssueSummary{"tk-1": {ID: "tk-1", Title: "open issue", Status: issues.StatusOpen}}},
			"tk-2": {SelectedID: "tk-2", NodeSummaries: map[string]issues.IssueSummary{"tk-2": {ID: "tk-2", Title: "blocked issue", Status: issues.StatusBlocked}}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen},
				{ID: "tk-2", Title: "blocked issue", Status: issues.StatusBlocked},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.handleKey("/")

	for _, ch := range "status=blocked" {
		m.handleKey(string(ch))
	}

	m.handleKey("enter")

	if got := m.filter.Status; got != "blocked" {
		t.Fatalf("unexpected filter status: %q", got)
	}

	if len(reader.listFilters) != 2 || reader.listFilters[1].Status != "blocked" {
		t.Fatalf("unexpected list filters: %#v", reader.listFilters)
	}

	if len(m.summaries) != 1 || m.summaries[0].ID != "tk-2" {
		t.Fatalf("unexpected filtered summaries: %#v", m.summaries)
	}

	header := m.renderHeader()
	if !strings.Contains(header, "filters status=blocked") || !strings.Contains(header, "results 1") {
		t.Fatalf("unexpected header after filter update: %s", header)
	}
}

func TestDetailsAndCommentsTabsRenderTypedDetailContext(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-2", Title: "target", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-2": {
				Issue: issues.Issue{
					ID:          "tk-2",
					Title:       "target",
					Status:      issues.StatusOpen,
					Type:        issues.TypeTask,
					Priority:    "medium",
					ParentID:    "tk-1",
					Description: "detail body",
				},
				Comments: []issues.Comment{
					{Author: "alice", Body: "needs follow-up"},
				},
				Dependencies: issues.DependencyList{
					IssueID:   "tk-2",
					BlockedBy: []issues.Link{{SourceID: "tk-3"}},
					Blocks:    []issues.Link{{TargetID: "tk-4"}},
				},
				RelatedSummaries: map[string]issues.IssueSummary{
					"tk-1": {ID: "tk-1", Title: "parent", Status: issues.StatusOpen},
					"tk-3": {ID: "tk-3", Title: "blocker", Status: issues.StatusBlocked},
					"tk-4": {ID: "tk-4", Title: "downstream", Status: issues.StatusOpen},
				},
			},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-2": {SelectedID: "tk-2", NodeSummaries: map[string]issues.IssueSummary{"tk-2": {ID: "tk-2", Title: "target", Status: issues.StatusOpen}}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-2", Title: "target", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	details := m.renderDetailsTab()
	if !strings.Contains(details, "tk-1  [open] parent") || !strings.Contains(details, "tk-3  [blocked] blocker") || !strings.Contains(details, "tk-4  [open] downstream") {
		t.Fatalf("details tab did not render related context:\n%s", details)
	}

	comments := m.renderCommentsTab()
	if !strings.Contains(comments, "1 comment(s)") || !strings.Contains(comments, "needs follow-up") {
		t.Fatalf("comments tab did not render typed comments:\n%s", comments)
	}
}

func TestEmptyAndCompactStatesRenderIntentionally(t *testing.T) {
	t.Parallel()

	emptyReader := &fakeReader{
		project: issues.ProjectGraphView{},
	}

	emptyModel, err := newModel(context.Background(), "/repo", emptyReader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	browser := emptyModel.renderBrowserBody()
	if !strings.Contains(browser, "No issues yet.") || !strings.Contains(browser, "tack create") || !strings.Contains(browser, "tack import") {
		t.Fatalf("unexpected empty repo browser state:\n%s", browser)
	}

	filteredReader := &fakeReader{
		listByFilter: func(store.ListFilter) []issues.IssueSummary {
			return nil
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "existing", Status: issues.StatusOpen}},
		},
	}

	filteredModel, err := newModel(context.Background(), "/repo", filteredReader, StartupOptions{
		Filter: store.ListFilter{Status: issues.StatusBlocked},
	})
	if err != nil {
		t.Fatal(err)
	}

	filteredBrowser := filteredModel.renderBrowserBody()
	if !strings.Contains(filteredBrowser, "No matching issues.") || !strings.Contains(filteredBrowser, "status=blocked") {
		t.Fatalf("unexpected filtered empty state:\n%s", filteredBrowser)
	}

	filteredModel.width = 50
	filteredModel.height = 10

	compact := filteredModel.render()
	if !strings.Contains(compact, "Compact single-column layout") || !strings.Contains(compact, "Issues") || !strings.Contains(compact, "Details") {
		t.Fatalf("unexpected compact layout:\n%s", compact)
	}
}

func TestCtrlRRefreshReopensStoreAndReloadsFromDisk(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	_, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "first",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	m, err := newModel(ctx, repo, s, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.closeReader()

	other, err := store.Open(filepath.Join(repo, ".tack", "issues.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeReader(other)

	_, err = other.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "second",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	m.handleKey("ctrl+r")

	if len(m.summaries) != 2 {
		t.Fatalf("expected refreshed summaries from disk, got %#v", m.summaries)
	}
}

type fakeReader struct {
	listCalls  int
	readyCalls int

	listFilters  []store.ListFilter
	readyFilters []store.ListFilter

	allSummaries   []issues.IssueSummary
	readySummaries []issues.IssueSummary
	listByFilter   func(store.ListFilter) []issues.IssueSummary
	readyByFilter  func(store.ListFilter) []issues.IssueSummary
	details        map[string]issues.IssueDetailView
	focused        map[string]issues.FocusedGraphView
	project        issues.ProjectGraphView
}

func (f *fakeReader) ListIssueSummaries(_ context.Context, filter store.ListFilter) ([]issues.IssueSummary, error) {
	f.listCalls++
	f.listFilters = append(f.listFilters, filter)

	if f.listByFilter != nil {
		return cloneSummaries(f.listByFilter(filter)), nil
	}

	return cloneSummaries(f.allSummaries), nil
}

func (f *fakeReader) ReadyIssueSummaries(_ context.Context, filter store.ListFilter) ([]issues.IssueSummary, error) {
	f.readyCalls++
	f.readyFilters = append(f.readyFilters, filter)

	if f.readyByFilter != nil {
		return cloneSummaries(f.readyByFilter(filter)), nil
	}

	return cloneSummaries(f.readySummaries), nil
}

func (f *fakeReader) IssueDetailView(_ context.Context, id string) (issues.IssueDetailView, error) {
	return f.details[id], nil
}

func (f *fakeReader) FocusedGraphView(_ context.Context, id string) (issues.FocusedGraphView, error) {
	return f.focused[id], nil
}

func (f *fakeReader) ProjectGraphView(context.Context) (issues.ProjectGraphView, error) {
	return f.project, nil
}

func (f *fakeReader) Close() error {
	return nil
}

func cloneSummaries(in []issues.IssueSummary) []issues.IssueSummary {
	if in == nil {
		return nil
	}

	out := make([]issues.IssueSummary, len(in))
	copy(out, in)

	return out
}
