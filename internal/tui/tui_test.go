package tui

import (
	"context"
	"testing"

	"tack/internal/issues"
	"tack/internal/store"
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

type fakeReader struct {
	listCalls  int
	readyCalls int

	allSummaries   []issues.IssueSummary
	readySummaries []issues.IssueSummary
	details        map[string]issues.IssueDetailView
	focused        map[string]issues.FocusedGraphView
	project        issues.ProjectGraphView
}

func (f *fakeReader) ListIssueSummaries(context.Context, store.ListFilter) ([]issues.IssueSummary, error) {
	f.listCalls++

	return cloneSummaries(f.allSummaries), nil
}

func (f *fakeReader) ReadyIssueSummaries(context.Context, store.ListFilter) ([]issues.IssueSummary, error) {
	f.readyCalls++

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
