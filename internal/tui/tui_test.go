package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"tack/internal/issues"
	"tack/internal/store"
	"tack/internal/testutil"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

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

	if cmd := m.Init(); cmd == nil {
		t.Fatalf("expected Init to schedule auto-refresh")
	}
}

func TestNewModelDefaultsAllModeToActiveStatuses(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		listByFilter: func(filter store.ListFilter) []issues.IssueSummary {
			if !slices.Equal(filter.Statuses, defaultAllIssueStatuses) {
				t.Fatalf("expected default active statuses, got %#v", filter.Statuses)
			}

			return []issues.IssueSummary{
				{ID: "tk-1", Title: "active issue", Status: issues.StatusOpen, Type: issues.TypeTask},
			}
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "active issue", Status: issues.StatusOpen}},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-1": {SelectedID: "tk-1", NodeSummaries: map[string]issues.IssueSummary{"tk-1": {ID: "tk-1", Title: "active issue", Status: issues.StatusOpen}}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "active issue", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(reader.listFilters) != 1 || !slices.Equal(reader.listFilters[0].Statuses, defaultAllIssueStatuses) {
		t.Fatalf("expected startup load to use default statuses, got %#v", reader.listFilters)
	}

	if summary := formatFilter(m.effectiveFilter()); summary != "status=open,in_progress,blocked" {
		t.Fatalf("expected default filter summary, got %q", summary)
	}

	if header := m.renderHeader(); !strings.Contains(header, "filter status=open,in_progress,blocked") {
		t.Fatalf("expected header to describe default filter, got %q", header)
	}

	m.refresh()

	if len(reader.listFilters) != 2 || !slices.Equal(reader.listFilters[1].Statuses, defaultAllIssueStatuses) {
		t.Fatalf("expected refresh to preserve default statuses, got %#v", reader.listFilters)
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

	m.handleKey("right")

	if m.focus != paneDetail || m.activeTab != tabDetails {
		t.Fatalf("expected detail pane focus, got focus=%s tab=%d", m.focus, m.activeTab)
	}

	m.handleKey("right")

	if m.activeTab != tabComments {
		t.Fatalf("expected activity tab, got %d", m.activeTab)
	}

	m.handleKey("right")

	if m.activeTab != tabProjectGraph {
		t.Fatalf("expected project graph tab, got %d", m.activeTab)
	}

	m.handleKey("left")

	if m.activeTab != tabComments {
		t.Fatalf("expected activity tab after reverse, got %d", m.activeTab)
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

func TestArrowNavigationWrapsAcrossDetailTabs(t *testing.T) {
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
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-1", Title: "first", Status: issues.StatusOpen, Type: issues.TypeTask},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.handleKey("right")
	m.handleKey("right")
	m.handleKey("right")

	if m.focus != paneDetail || m.activeTab != tabProjectGraph {
		t.Fatalf("expected project graph after advancing with right, got focus=%s tab=%d", m.focus, m.activeTab)
	}

	m.handleKey("right")

	if m.focus != paneDetail || m.activeTab != tabDetails {
		t.Fatalf("expected wrap back to details after project graph, got focus=%s tab=%d", m.focus, m.activeTab)
	}

	m.handleKey("left")

	if m.focus != paneBrowser || m.activeTab != tabDetails {
		t.Fatalf("expected reverse from details to browser, got focus=%s tab=%d", m.focus, m.activeTab)
	}
}

func TestArrowKeysMirrorTabNavigation(t *testing.T) {
	t.Parallel()

	buildModel := func(t *testing.T) *model {
		t.Helper()

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
			project: issues.ProjectGraphView{
				Issues: []issues.IssueSummary{
					{ID: "tk-1", Title: "first", Status: issues.StatusOpen, Type: issues.TypeTask},
				},
			},
		}

		m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
		if err != nil {
			t.Fatal(err)
		}

		return m
	}

	testCases := []struct {
		name          string
		startFocus    paneFocus
		startTab      detailTab
		firstKey      string
		secondKey     string
		expectedFocus paneFocus
		expectedTab   detailTab
	}{
		{
			name:          "right matches tab from browser",
			startFocus:    paneBrowser,
			startTab:      tabDetails,
			firstKey:      "right",
			secondKey:     "tab",
			expectedFocus: paneDetail,
			expectedTab:   tabDetails,
		},
		{
			name:          "right matches tab inside detail tabs",
			startFocus:    paneDetail,
			startTab:      tabComments,
			firstKey:      "right",
			secondKey:     "tab",
			expectedFocus: paneDetail,
			expectedTab:   tabProjectGraph,
		},
		{
			name:          "left matches shift+tab from details",
			startFocus:    paneDetail,
			startTab:      tabDetails,
			firstKey:      "left",
			secondKey:     "shift+tab",
			expectedFocus: paneBrowser,
			expectedTab:   tabDetails,
		},
		{
			name:          "left matches shift+tab from browser",
			startFocus:    paneBrowser,
			startTab:      tabDetails,
			firstKey:      "left",
			secondKey:     "shift+tab",
			expectedFocus: paneDetail,
			expectedTab:   tabProjectGraph,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			arrowModel := buildModel(t)
			arrowModel.focus = tc.startFocus
			arrowModel.activeTab = tc.startTab
			arrowModel.handleKey(tc.firstKey)

			tabModel := buildModel(t)
			tabModel.focus = tc.startFocus
			tabModel.activeTab = tc.startTab
			tabModel.handleKey(tc.secondKey)

			if arrowModel.focus != tc.expectedFocus || arrowModel.activeTab != tc.expectedTab {
				t.Fatalf("unexpected arrow result: focus=%s tab=%d", arrowModel.focus, arrowModel.activeTab)
			}

			if arrowModel.focus != tabModel.focus || arrowModel.activeTab != tabModel.activeTab {
				t.Fatalf("expected %q to match %q, got arrow focus=%s tab=%d and tab focus=%s tab=%d", tc.firstKey, tc.secondKey, arrowModel.focus, arrowModel.activeTab, tabModel.focus, tabModel.activeTab)
			}
		})
	}
}

func TestHelpTextMentionsArrowNavigationAndGraphPanning(t *testing.T) {
	t.Parallel()

	m := &model{}

	footer := m.renderFooter()
	if !strings.Contains(footer, "tab/left/right switch") || !strings.Contains(footer, "/ filters") {
		t.Fatalf("expected footer to mention left/right switching, got %q", footer)
	}

	help := m.renderExpandedHelp()
	if !strings.Contains(help, "tab/shift+tab and left/right switch panes or tabs") {
		t.Fatalf("expected help to mention left/right navigation, got:\n%s", help)
	}

	if !strings.Contains(help, "g or G opens Project Graph") {
		t.Fatalf("expected help to mention the single graph tab, got:\n%s", help)
	}

	if !strings.Contains(help, "space toggles the highlighted value") || !strings.Contains(help, "empty value clears the limit") {
		t.Fatalf("expected help to mention guided filter controls, got:\n%s", help)
	}

	if !strings.Contains(help, "h/l pan horizontally") {
		t.Fatalf("expected help to mention h/l graph panning, got:\n%s", help)
	}
}

func TestGraphTabKeepsHorizontalPanningOnHAndLOnly(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-2", Title: "Selected issue", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-2": {Issue: issues.Issue{ID: "tk-2", Title: "Selected issue", Status: issues.StatusOpen}},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-2": {
				SelectedID:   "tk-2",
				ParentID:     "tk-1",
				BlockedByIDs: []string{"tk-3"},
				BlocksIDs:    []string{"tk-4"},
				ChildIDs:     []string{"tk-5"},
				NodeSummaries: map[string]issues.IssueSummary{
					"tk-1": {ID: "tk-1", Title: "Parent epic", Status: issues.StatusClosed, Type: issues.TypeEpic},
					"tk-2": {ID: "tk-2", Title: "Selected issue", Status: issues.StatusOpen, Type: issues.TypeTask},
					"tk-3": {ID: "tk-3", Title: "Blocking bug", Status: issues.StatusBlocked, Type: issues.TypeBug},
					"tk-4": {ID: "tk-4", Title: "Downstream feature", Status: issues.StatusOpen, Type: issues.TypeFeature},
					"tk-5": {ID: "tk-5", Title: "Child task", Status: issues.StatusOpen, Type: issues.TypeTask},
				},
			},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-2", Title: "Selected issue", Status: issues.StatusOpen, Type: issues.TypeTask},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.focus = paneDetail
	m.activeTab = tabProjectGraph

	m.handleKey("l")

	if m.projectGraphViewport.x == 0 {
		t.Fatalf("expected l to pan the project graph horizontally, got %#v", m.projectGraphViewport)
	}

	xAfterL := m.projectGraphViewport.x
	m.handleKey("right")

	if m.activeTab != tabDetails {
		t.Fatalf("expected right to wrap back to details, got tab=%d", m.activeTab)
	}

	if m.projectGraphViewport.x != xAfterL {
		t.Fatalf("expected right to leave project graph pan unchanged, got %#v", m.projectGraphViewport)
	}

	m.handleKey("g")

	if m.activeTab != tabProjectGraph {
		t.Fatalf("expected g to reopen the project graph, got tab=%d", m.activeTab)
	}

	xBeforeH := m.projectGraphViewport.x
	m.handleKey("h")

	if m.projectGraphViewport.x >= xBeforeH {
		t.Fatalf("expected h to pan the project graph back left, before=%d after=%d", xBeforeH, m.projectGraphViewport.x)
	}

	m.activeTab = tabDetails
	m.handleKey("G")

	if m.activeTab != tabProjectGraph {
		t.Fatalf("expected G to open the project graph, got tab=%d", m.activeTab)
	}
}

func TestGuidedFilterPickerUpdatesFilterAndSummaries(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		listByFilter: func(filter store.ListFilter) []issues.IssueSummary {
			if len(filter.Statuses) == 1 && filter.Statuses[0] == "blocked" {
				return []issues.IssueSummary{
					{ID: "tk-2", Title: "blocked issue", Status: issues.StatusBlocked, Type: issues.TypeBug},
				}
			}

			return []issues.IssueSummary{
				{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen, Type: issues.TypeTask},
				{ID: "tk-2", Title: "blocked issue", Status: issues.StatusBlocked, Type: issues.TypeBug},
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
	m.handleKey("enter")
	m.handleKey("space")
	m.handleKey("down")
	m.handleKey("space")
	m.handleKey("down")
	m.handleKey("enter")

	if len(m.filter.Statuses) != 1 || m.filter.Statuses[0] != "blocked" {
		t.Fatalf("unexpected filter status: %#v", m.filter.Statuses)
	}

	if len(reader.listFilters) != 2 || len(reader.listFilters[1].Statuses) != 1 || reader.listFilters[1].Statuses[0] != "blocked" {
		t.Fatalf("unexpected list filters: %#v", reader.listFilters)
	}

	if len(m.summaries) != 1 || m.summaries[0].ID != "tk-2" {
		t.Fatalf("unexpected filtered summaries: %#v", m.summaries)
	}

	header := m.renderHeader()
	if !strings.Contains(header, "filter status=blocked") || !strings.Contains(header, "1 issues") {
		t.Fatalf("unexpected header after filter update: %s", header)
	}
}

func TestGuidedStatusFilterPickerShowsAllValuesAndMarksImplicitDefaultSelected(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen, Type: issues.TypeTask},
			{ID: "tk-2", Title: "closed issue", Status: issues.StatusClosed, Type: issues.TypeBug},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen},
				{ID: "tk-2", Title: "closed issue", Status: issues.StatusClosed},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.handleKey("/")
	m.handleKey("enter")

	if !slices.Equal(m.filterPicker.values, []string{issues.StatusOpen, issues.StatusInProgress, issues.StatusBlocked, issues.StatusClosed}) {
		t.Fatalf("expected status picker to show all statuses, got %#v", m.filterPicker.values)
	}

	for _, status := range defaultAllIssueStatuses {
		if _, ok := m.filterPicker.selected[status]; !ok {
			t.Fatalf("expected default status %q to be selected, got %#v", status, m.filterPicker.selected)
		}
	}

	if _, ok := m.filterPicker.selected[issues.StatusClosed]; ok {
		t.Fatalf("expected non-default closed status to remain unselected, got %#v", m.filterPicker.selected)
	}
}

func TestGuidedStatusFilterPickerTogglesImplicitDefaultNormally(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		listByFilter: func(filter store.ListFilter) []issues.IssueSummary {
			if slices.Equal(filter.Statuses, []string{issues.StatusInProgress, issues.StatusBlocked}) {
				return []issues.IssueSummary{
					{ID: "tk-2", Title: "active issue", Status: issues.StatusBlocked, Type: issues.TypeBug},
				}
			}

			return []issues.IssueSummary{
				{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen, Type: issues.TypeTask},
				{ID: "tk-2", Title: "active issue", Status: issues.StatusBlocked, Type: issues.TypeBug},
			}
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen}},
			"tk-2": {Issue: issues.Issue{ID: "tk-2", Title: "active issue", Status: issues.StatusBlocked}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-1", Title: "open issue", Status: issues.StatusOpen},
				{ID: "tk-2", Title: "active issue", Status: issues.StatusBlocked},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.handleKey("/")
	m.handleKey("enter")
	m.handleKey("space")
	m.handleKey("enter")

	if !slices.Equal(m.filter.Statuses, []string{issues.StatusInProgress, issues.StatusBlocked}) {
		t.Fatalf("expected only the toggled status to change, got %#v", m.filter.Statuses)
	}

	if len(reader.listFilters) < 2 || !slices.Equal(reader.listFilters[len(reader.listFilters)-1].Statuses, []string{issues.StatusInProgress, issues.StatusBlocked}) {
		t.Fatalf("expected reload to keep the remaining selected defaults, got %#v", reader.listFilters)
	}
}

func TestGuidedFilterPickerSupportsMultiSelect(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		filterValuesByRequest: func(source store.FilterValueSource, key store.FilterValueKey, filter store.ListFilter) []string {
			if source != store.FilterValueSourceAll {
				t.Fatalf("unexpected source: %q", source)
			}

			if key != store.FilterValueKeyLabel {
				t.Fatalf("unexpected key: %q", key)
			}

			return []string{"api", "ops", "docs"}
		},
		listByFilter: func(filter store.ListFilter) []issues.IssueSummary {
			if len(filter.Labels) == 2 && filter.Labels[0] == "api" && filter.Labels[1] == "ops" {
				return []issues.IssueSummary{
					{ID: "tk-2", Title: "matching issue", Status: issues.StatusOpen, Type: issues.TypeTask},
				}
			}

			return []issues.IssueSummary{
				{ID: "tk-1", Title: "all issue", Status: issues.StatusOpen, Type: issues.TypeTask},
				{ID: "tk-2", Title: "matching issue", Status: issues.StatusOpen, Type: issues.TypeTask},
			}
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "all issue", Status: issues.StatusOpen}},
			"tk-2": {Issue: issues.Issue{ID: "tk-2", Title: "matching issue", Status: issues.StatusOpen}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-1", Title: "all issue", Status: issues.StatusOpen},
				{ID: "tk-2", Title: "matching issue", Status: issues.StatusOpen},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.handleKey("/")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("enter")
	m.handleKey("space")
	m.handleKey("down")
	m.handleKey("space")
	m.handleKey("enter")

	if len(m.filter.Labels) != 2 || m.filter.Labels[0] != "api" || m.filter.Labels[1] != "ops" {
		t.Fatalf("unexpected filter labels: %#v", m.filter.Labels)
	}

	applied := reader.listFilters[len(reader.listFilters)-1]
	if len(applied.Labels) != 2 || applied.Labels[0] != "api" || applied.Labels[1] != "ops" {
		t.Fatalf("unexpected applied filter: %#v", applied)
	}

	if summary := m.renderFilterPickerKeySummary(filterPickerKeyLabel); !strings.Contains(summary, "api, ops") {
		t.Fatalf("expected multi-select summary to be visible, got %q", summary)
	}
}

func TestGuidedLabelFilterPickerUsesMultiColumnLayoutWhenWide(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "issue", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		filterValuesByRequest: func(source store.FilterValueSource, key store.FilterValueKey, filter store.ListFilter) []string {
			if key != store.FilterValueKeyLabel {
				t.Fatalf("unexpected key: %q", key)
			}

			return []string{"label-01", "label-02", "label-03", "label-04", "label-05", "label-06"}
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "issue", Status: issues.StatusOpen}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "issue", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.width = 64
	m.height = 16
	m.handleKey("/")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("enter")

	layout := m.currentFilterPickerValueLayout()
	if !layout.multiColumn || layout.columns != 3 || layout.rows != 2 {
		t.Fatalf("expected a 3x2 multi-column layout, got %#v", layout)
	}

	rendered := ansiPattern.ReplaceAllString(m.render(), "")
	foundRow := false

	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "label-01") && strings.Contains(line, "label-03") && strings.Contains(line, "label-05") {
			foundRow = true
			break
		}
	}

	if !foundRow {
		t.Fatalf("expected multi-column row in rendered picker, got:\n%s", rendered)
	}
}

func TestGuidedLabelFilterPickerLeftRightMovesBetweenColumns(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "issue", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		filterValuesByRequest: func(source store.FilterValueSource, key store.FilterValueKey, filter store.ListFilter) []string {
			if key != store.FilterValueKeyLabel {
				t.Fatalf("unexpected key: %q", key)
			}

			return []string{"label-01", "label-02", "label-03", "label-04", "label-05", "label-06"}
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "issue", Status: issues.StatusOpen}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "issue", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.width = 64
	m.height = 16
	m.handleKey("/")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("enter")

	layout := m.currentFilterPickerValueLayout()
	if !layout.multiColumn {
		t.Fatalf("expected multi-column label layout, got %#v", layout)
	}

	m.handleKey("right")

	if m.filterPicker.valueIndex != layout.rows {
		t.Fatalf("expected right arrow to move to the next column, got index %d with layout %#v", m.filterPicker.valueIndex, layout)
	}

	m.handleKey("right")

	if m.filterPicker.valueIndex != layout.rows*2 {
		t.Fatalf("expected another right arrow to move again, got index %d with layout %#v", m.filterPicker.valueIndex, layout)
	}

	m.handleKey("left")

	if m.filterPicker.valueIndex != layout.rows {
		t.Fatalf("expected left arrow to move back a column, got index %d with layout %#v", m.filterPicker.valueIndex, layout)
	}
}

func TestGuidedFilterPickerScrollsLongValueLists(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "issue", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		filterValuesByRequest: func(source store.FilterValueSource, key store.FilterValueKey, filter store.ListFilter) []string {
			if key != store.FilterValueKeyLabel {
				t.Fatalf("unexpected key: %q", key)
			}

			values := make([]string, 0, 12)
			for i := 1; i <= 12; i++ {
				values = append(values, "label-"+fmt.Sprintf("%02d", i))
			}

			return values
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "issue", Status: issues.StatusOpen}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "issue", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.width = 80
	m.height = 12

	m.handleKey("/")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("enter")

	for i := 0; i < 9; i++ {
		m.handleKey("down")
	}

	rendered := ansiPattern.ReplaceAllString(m.render(), "")
	if lipgloss.Height(rendered) > m.height {
		t.Fatalf("expected guided filter panel to stay within the terminal height, got %d lines for height %d:\n%s", lipgloss.Height(rendered), m.height, rendered)
	}

	if !strings.Contains(rendered, "> [ ] label-10") {
		t.Fatalf("expected the selected off-screen value to remain visible, got:\n%s", rendered)
	}
}

func TestGuidedFilterPickerLimitAndReset(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "issue", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "issue", Status: issues.StatusOpen}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "issue", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{
		Filter: store.ListFilter{
			Statuses: []string{issues.StatusOpen},
			Limit:    3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	m.handleKey("/")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("enter")
	m.handleKey("backspace")
	m.handleKey("enter")

	if m.filter.Limit != 0 {
		t.Fatalf("expected empty limit input to clear the limit, got %#v", m.filter)
	}

	m.handleKey("/")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("down")
	m.handleKey("enter")

	if !slices.Equal(m.effectiveFilter().Statuses, defaultAllIssueStatuses) || m.filter.Limit != 0 {
		t.Fatalf("expected reset to restore default active statuses and clear extras, got %#v (effective %#v)", m.filter, m.effectiveFilter())
	}

	last := reader.listFilters[len(reader.listFilters)-1]
	if !slices.Equal(last.Statuses, defaultAllIssueStatuses) || last.Limit != 0 || len(last.Labels) != 0 || len(last.Types) != 0 || len(last.Assignees) != 0 {
		t.Fatalf("expected reset reload to restore default statuses and clear extras, got %#v", last)
	}
}

func TestGuidedFilterPickerReadyModeOmitsAssigneeAndClearsExistingValues(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		readySummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "ready", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "ready", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{
		Filter: store.ListFilter{Assignees: []string{"alice"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(m.filter.Assignees) != 1 || m.filter.Assignees[0] != "alice" {
		t.Fatalf("expected initial assignee filter to be present, got %#v", m.filter.Assignees)
	}

	m.handleKey("r")

	if m.source != DataSourceReady {
		t.Fatalf("expected ready source after toggle, got %q", m.source)
	}

	if len(m.filter.Assignees) != 0 {
		t.Fatalf("expected ready mode to clear assignee filters, got %#v", m.filter.Assignees)
	}

	if len(reader.readyFilters) == 0 || len(reader.readyFilters[len(reader.readyFilters)-1].Statuses) != 0 {
		t.Fatalf("expected ready mode to avoid inheriting the all-issues default status filter, got %#v", reader.readyFilters)
	}

	m.handleKey("/")

	for _, option := range m.filterPickerOptions() {
		if option.key == filterPickerKeyAssignee {
			t.Fatalf("assignee should be omitted from ready-mode picker options: %#v", m.filterPickerOptions())
		}
	}
}

func TestFilterOptionsUseCurrentSourceAndFilter(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		readySummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "ready", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		filterValuesByRequest: func(source store.FilterValueSource, key store.FilterValueKey, filter store.ListFilter) []string {
			if source != store.FilterValueSourceReady {
				t.Fatalf("unexpected source: %q", source)
			}

			if key != store.FilterValueKeyLabel {
				t.Fatalf("unexpected key: %q", key)
			}

			if len(filter.Statuses) != 1 || filter.Statuses[0] != issues.StatusOpen {
				t.Fatalf("unexpected filter statuses: %#v", filter)
			}

			if len(filter.Types) != 1 || filter.Types[0] != issues.TypeTask {
				t.Fatalf("unexpected filter types: %#v", filter)
			}

			return []string{"backend", "ui"}
		},
		project: issues.ProjectGraphView{},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{
		Source: DataSourceReady,
		Filter: store.ListFilter{
			Statuses: []string{issues.StatusOpen},
			Types:    []string{issues.TypeTask},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	values, err := m.filterOptions(store.FilterValueKeyLabel)
	if err != nil {
		t.Fatal(err)
	}

	if len(values) != 2 || values[0] != "backend" || values[1] != "ui" {
		t.Fatalf("unexpected filter values: %#v", values)
	}
}

func TestDetailsAndActivityTabsRenderTypedDetailContext(t *testing.T) {
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
					Description: "# Context\n\n- detail body\n- next step",
				},
				Comments: []issues.Comment{
					{
						ID:        21,
						Author:    "alice",
						Body:      "needs follow-up",
						CreatedAt: mustParseTime(t, "2026-04-14T10:05:00Z"),
					},
				},
				Events: []issues.Event{
					{
						ID:        11,
						Actor:     "alice",
						EventType: "issue_created",
						CreatedAt: mustParseTime(t, "2026-04-14T10:00:00Z"),
					},
					{
						ID:        12,
						Actor:     "alice",
						EventType: "issue_updated",
						Payload:   `{"status":"in_progress","assignee":"alice","claim":true}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:03:00Z"),
					},
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
				LatestCloseReason:  "done and verified",
				LatestReopenReason: "follow-up requested",
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

	details := m.renderDetailsTab(72)
	if !strings.Contains(details, "tk-1  [open] parent") || !strings.Contains(details, "tk-3  [blocked] blocker") || !strings.Contains(details, "tk-4  [open] downstream") {
		t.Fatalf("details tab did not render related context:\n%s", details)
	}

	if !strings.Contains(details, "close reason") || !strings.Contains(details, "done and verified") || !strings.Contains(details, "reopen reason") || !strings.Contains(details, "follow-up requested") {
		t.Fatalf("details tab did not render transition reasons:\n%s", details)
	}

	plainDetails := ansiPattern.ReplaceAllString(details, "")
	if !strings.Contains(plainDetails, "Context") || !strings.Contains(plainDetails, "detail body") || strings.Contains(plainDetails, "# Context") {
		t.Fatalf("details tab did not render markdown description cleanly:\n%s", plainDetails)
	}

	activity := m.renderActivityTab()

	plainActivity := ansiPattern.ReplaceAllString(activity, "")
	if !strings.Contains(plainActivity, "3 item(s)") || !strings.Contains(plainActivity, "alice created the issue") || !strings.Contains(plainActivity, "alice claimed the issue, set status to in_progress, and set assignee to alice") || !strings.Contains(plainActivity, "needs follow-up") {
		t.Fatalf("activity tab did not render merged activity cleanly:\n%s", plainActivity)
	}
}

func TestActivityTabMergesCommentsAndEventsChronologically(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "target", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {
				Issue: issues.Issue{ID: "tk-1", Title: "target", Status: issues.StatusOpen, Type: issues.TypeTask},
				Comments: []issues.Comment{
					{
						ID:        22,
						Author:    "alice",
						Body:      "body from the real comment",
						CreatedAt: mustParseTime(t, "2026-04-14T10:02:00Z"),
					},
				},
				Events: []issues.Event{
					{
						ID:        10,
						Actor:     "alice",
						EventType: "issue_created",
						CreatedAt: mustParseTime(t, "2026-04-14T10:00:00Z"),
					},
					{
						ID:        11,
						Actor:     "alice",
						EventType: "comment_added",
						Payload:   `{"body":"body from the real comment"}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:02:00Z"),
					},
					{
						ID:        12,
						Actor:     "alice",
						EventType: "labels_replaced",
						Payload:   `{"labels":["backend","tui"]}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:03:00Z"),
					},
					{
						ID:        13,
						Actor:     "alice",
						EventType: "mystery_event",
						CreatedAt: mustParseTime(t, "2026-04-14T10:04:00Z"),
					},
				},
			},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "target", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rendered := ansiPattern.ReplaceAllString(m.renderActivityTab(), "")

	indexes := []int{
		strings.Index(rendered, "alice created the issue"),
		strings.Index(rendered, "alice added a comment"),
		strings.Index(rendered, "body from the real comment"),
		strings.Index(rendered, "alice replaced labels with: backend, tui"),
		strings.Index(rendered, "alice recorded mystery event"),
	}
	for i, idx := range indexes {
		if idx < 0 {
			t.Fatalf("expected activity entry %d in render:\n%s", i, rendered)
		}
	}

	for i := 1; i < len(indexes); i++ {
		if indexes[i-1] >= indexes[i] {
			t.Fatalf("expected chronological activity ordering, got:\n%s", rendered)
		}
	}
}

func TestActivityTabHumanizesRepresentativeEventsWithoutPayloadNoise(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "target", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {
				Issue: issues.Issue{ID: "tk-1", Title: "target", Status: issues.StatusOpen, Type: issues.TypeTask},
				Events: []issues.Event{
					{
						ID:        10,
						Actor:     "alice",
						EventType: "issue_created",
						CreatedAt: mustParseTime(t, "2026-04-14T10:00:00Z"),
					},
					{
						ID:        11,
						Actor:     "alice",
						EventType: "issue_updated",
						Payload:   `{"assignee":"bob","description":"new body"}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:01:00Z"),
					},
					{
						ID:        12,
						Actor:     "alice",
						EventType: "issue_closed",
						Payload:   `{"reason":"done and verified"}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:02:00Z"),
					},
					{
						ID:        13,
						Actor:     "alice",
						EventType: "issue_reopened",
						Payload:   `{"reason":"needed another pass"}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:03:00Z"),
					},
					{
						ID:        14,
						Actor:     "alice",
						EventType: "dependency_added",
						Payload:   `{"blocker_id":"tk-9"}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:04:00Z"),
					},
					{
						ID:        15,
						Actor:     "alice",
						EventType: "dependency_removed",
						Payload:   `{"blocker_id":"tk-9"}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:05:00Z"),
					},
					{
						ID:        16,
						Actor:     "alice",
						EventType: "labels_added",
						Payload:   `{"labels":["backend"]}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:06:00Z"),
					},
					{
						ID:        17,
						Actor:     "alice",
						EventType: "labels_removed",
						Payload:   `{"labels":["backend"]}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:07:00Z"),
					},
					{
						ID:        18,
						Actor:     "alice",
						EventType: "labels_replaced",
						Payload:   `{"labels":["cli","tui"]}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:08:00Z"),
					},
				},
			},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "target", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rendered := ansiPattern.ReplaceAllString(m.renderActivityTab(), "")

	expectedSnippets := []string{
		"alice created the issue",
		"alice set assignee to bob and updated the description",
		"alice closed the issue (done and verified)",
		"alice reopened the issue (needed another pass)",
		"alice added dependency on tk-9",
		"alice removed dependency on tk-9",
		"alice added labels: backend",
		"alice removed labels: backend",
		"alice replaced labels with: cli, tui",
	}
	for _, snippet := range expectedSnippets {
		if !strings.Contains(rendered, snippet) {
			t.Fatalf("expected %q in activity render:\n%s", snippet, rendered)
		}
	}

	forbiddenSnippets := []string{`"reason"`, `blocker_id`, `{"labels"`, `{"assignee"`, `["backend"]`}
	for _, snippet := range forbiddenSnippets {
		if strings.Contains(rendered, snippet) {
			t.Fatalf("expected activity render to omit raw payload snippet %q:\n%s", snippet, rendered)
		}
	}
}

func TestActivityTabShowsEmptyState(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "target", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: "target", Status: issues.StatusOpen, Type: issues.TypeTask}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "target", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rendered := ansiPattern.ReplaceAllString(m.renderActivityTab(), "")
	if !strings.Contains(rendered, "0 item(s)") || !strings.Contains(rendered, "No activity yet.") {
		t.Fatalf("expected activity empty state, got:\n%s", rendered)
	}
}

func TestActivityPaneScrollsLongActivityWhenFocused(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "first", Status: issues.StatusOpen, Type: issues.TypeTask},
			{ID: "tk-2", Title: "second", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {
				Issue: issues.Issue{
					ID:     "tk-1",
					Title:  "first",
					Status: issues.StatusOpen,
					Type:   issues.TypeTask,
				},
				Comments: []issues.Comment{
					{
						ID:        21,
						Author:    "alice",
						Body:      "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12",
						CreatedAt: mustParseTime(t, "2026-04-14T10:05:00Z"),
					},
				},
				Events: []issues.Event{
					{
						ID:        11,
						Actor:     "alice",
						EventType: "issue_created",
						CreatedAt: mustParseTime(t, "2026-04-14T10:00:00Z"),
					},
					{
						ID:        12,
						Actor:     "alice",
						EventType: "labels_replaced",
						Payload:   `{"labels":["backend","tui"]}`,
						CreatedAt: mustParseTime(t, "2026-04-14T10:06:00Z"),
					},
				},
			},
			"tk-2": {
				Issue: issues.Issue{
					ID:     "tk-2",
					Title:  "second",
					Status: issues.StatusOpen,
					Type:   issues.TypeTask,
				},
			},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-1": {SelectedID: "tk-1", NodeSummaries: map[string]issues.IssueSummary{"tk-1": {ID: "tk-1", Title: "first", Status: issues.StatusOpen}}},
			"tk-2": {SelectedID: "tk-2", NodeSummaries: map[string]issues.IssueSummary{"tk-2": {ID: "tk-2", Title: "second", Status: issues.StatusOpen}}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-1", Title: "first", Status: issues.StatusOpen, Type: issues.TypeTask},
				{ID: "tk-2", Title: "second", Status: issues.StatusOpen, Type: issues.TypeTask},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.focus = paneDetail
	m.activeTab = tabComments

	initial := ansiPattern.ReplaceAllString(m.renderActiveTabBody(60, 10), "")
	selectedBefore := m.selected

	if !strings.Contains(initial, "line 1") {
		t.Fatalf("expected initial activity viewport to show the top of the comment:\n%s", initial)
	}

	m.handleKey("ctrl+d")
	m.handleKey("ctrl+d")

	if m.selected != selectedBefore {
		t.Fatalf("activity viewport scroll should not move browser selection: before=%d after=%d", selectedBefore, m.selected)
	}

	if m.commentsViewport.y == 0 {
		t.Fatalf("expected activity viewport to scroll, got %#v", m.commentsViewport)
	}

	scrolled := ansiPattern.ReplaceAllString(m.renderActiveTabBody(60, 10), "")
	if initial == scrolled {
		t.Fatalf("expected activity viewport render to change after scrolling:\n%s", scrolled)
	}

	if strings.Contains("\n"+scrolled+"\n", "\nline 1\n") {
		t.Fatalf("expected early comment lines to scroll out of view, got:\n%s", scrolled)
	}

	m.focus = paneBrowser
	m.handleKey("j")

	if m.selected != 1 {
		t.Fatalf("expected browser selection to move once focus returned, got %d", m.selected)
	}

	if m.commentsViewport.y != 0 {
		t.Fatalf("expected activity viewport reset after selection changed, got %#v", m.commentsViewport)
	}
}

func TestDetailsTabOmitsEmptyTransitionReasons(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "target", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {
				Issue: issues.Issue{
					ID:       "tk-1",
					Title:    "target",
					Status:   issues.StatusOpen,
					Type:     issues.TypeTask,
					Priority: "medium",
				},
			},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{{ID: "tk-1", Title: "target", Status: issues.StatusOpen}},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	details := m.renderDetailsTab(72)
	if strings.Contains(details, "close reason") || strings.Contains(details, "reopen reason") {
		t.Fatalf("details tab should omit empty transition reason rows:\n%s", details)
	}
}

func TestDetailPaneScrollsLongDescriptionsWhenFocused(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-1", Title: "first", Status: issues.StatusOpen, Type: issues.TypeTask},
			{ID: "tk-2", Title: "second", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-1": {
				Issue: issues.Issue{
					ID:          "tk-1",
					Title:       "first",
					Status:      issues.StatusOpen,
					Type:        issues.TypeTask,
					Description: "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9",
				},
			},
			"tk-2": {
				Issue: issues.Issue{
					ID:          "tk-2",
					Title:       "second",
					Status:      issues.StatusOpen,
					Type:        issues.TypeTask,
					Description: "other issue",
				},
			},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-1": {SelectedID: "tk-1", NodeSummaries: map[string]issues.IssueSummary{"tk-1": {ID: "tk-1", Title: "first", Status: issues.StatusOpen}}},
			"tk-2": {SelectedID: "tk-2", NodeSummaries: map[string]issues.IssueSummary{"tk-2": {ID: "tk-2", Title: "second", Status: issues.StatusOpen}}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-1", Title: "first", Status: issues.StatusOpen},
				{ID: "tk-2", Title: "second", Status: issues.StatusOpen},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.focus = paneDetail
	m.activeTab = tabDetails

	initial := ansiPattern.ReplaceAllString(m.renderActiveTabBody(60, 8), "")
	selectedBefore := m.selected

	m.handleKey("j")
	m.handleKey("j")

	if m.selected != selectedBefore {
		t.Fatalf("detail viewport scroll should not move browser selection: before=%d after=%d", selectedBefore, m.selected)
	}

	if m.detailsViewport.y == 0 {
		t.Fatalf("expected detail viewport to scroll, got %#v", m.detailsViewport)
	}

	scrolled := ansiPattern.ReplaceAllString(m.renderActiveTabBody(60, 8), "")
	if initial == scrolled {
		t.Fatalf("expected detail viewport render to change after scrolling:\n%s", scrolled)
	}

	m.focus = paneBrowser
	m.handleKey("j")

	if m.selected != 1 {
		t.Fatalf("expected browser selection to move once focus returned, got %d", m.selected)
	}

	if m.detailsViewport.y != 0 {
		t.Fatalf("expected detail viewport reset after selection changed, got %#v", m.detailsViewport)
	}
}

func TestBrowserPaneScrollsLongIssueLists(t *testing.T) {
	t.Parallel()

	summaries := make([]issues.IssueSummary, 0, 8)
	details := make(map[string]issues.IssueDetailView, 8)
	focused := make(map[string]issues.FocusedGraphView, 8)

	for i := 1; i <= 8; i++ {
		id := "tk-" + strconv.Itoa(i)
		summary := issues.IssueSummary{ID: id, Title: "issue " + id, Status: issues.StatusOpen, Type: issues.TypeTask}
		summaries = append(summaries, summary)
		details[id] = issues.IssueDetailView{Issue: issues.Issue{ID: id, Title: "issue " + id, Status: issues.StatusOpen}}
		focused[id] = issues.FocusedGraphView{SelectedID: id, NodeSummaries: map[string]issues.IssueSummary{id: summary}}
	}

	reader := &fakeReader{
		allSummaries: summaries,
		details:      details,
		focused:      focused,
		project:      issues.ProjectGraphView{Issues: summaries},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.selected = 6

	rendered := ansiPattern.ReplaceAllString(m.renderBrowserBody(80, 4), "")
	if !strings.Contains(rendered, "ID") || !strings.Contains(rendered, "tk-7") {
		t.Fatalf("expected browser viewport to keep the header and selected row visible, got:\n%s", rendered)
	}

	if strings.Contains(rendered, "tk-1") || strings.Contains(rendered, "tk-2") {
		t.Fatalf("expected browser viewport to scroll past the earliest rows, got:\n%s", rendered)
	}
}

func TestBrowserPaneOmitsTitleColumnAndTitleText(t *testing.T) {
	t.Parallel()

	summary := issues.IssueSummary{
		ID:     "tk-1",
		Title:  "this is a very long issue title that should never appear in the browser pane",
		Status: issues.StatusOpen,
		Type:   issues.TypeTask,
	}

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{summary},
		details: map[string]issues.IssueDetailView{
			"tk-1": {Issue: issues.Issue{ID: "tk-1", Title: summary.Title, Status: issues.StatusOpen}},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-1": {SelectedID: "tk-1", NodeSummaries: map[string]issues.IssueSummary{"tk-1": summary}},
		},
		project: issues.ProjectGraphView{Issues: []issues.IssueSummary{summary}},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rendered := ansiPattern.ReplaceAllString(m.renderBrowserBody(80, 4), "")

	if strings.Contains(rendered, "TITLE") {
		t.Fatalf("expected browser header to omit the title column, got:\n%s", rendered)
	}

	if strings.Contains(rendered, summary.Title) {
		t.Fatalf("expected browser rows to omit issue titles, got:\n%s", rendered)
	}

	if !strings.Contains(rendered, "ID") || !strings.Contains(rendered, "STATUS") || !strings.Contains(rendered, "TYPE") {
		t.Fatalf("expected browser header to keep id/status/type columns, got:\n%s", rendered)
	}

	if !strings.Contains(rendered, "tk-1") || !strings.Contains(rendered, "open") || !strings.Contains(rendered, "task") {
		t.Fatalf("expected browser row to keep issue metadata, got:\n%s", rendered)
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

	browser := emptyModel.renderBrowserBody(80, 8)
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
		Filter: store.ListFilter{Statuses: []string{issues.StatusBlocked}},
	})
	if err != nil {
		t.Fatal(err)
	}

	filteredBrowser := filteredModel.renderBrowserBody(80, 8)
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

func TestRenderReservesSpaceForFooterAndHelp(t *testing.T) {
	t.Parallel()

	summaries := make([]issues.IssueSummary, 0, 16)
	details := make(map[string]issues.IssueDetailView, 16)
	focused := make(map[string]issues.FocusedGraphView, 16)

	for i := 1; i <= 16; i++ {
		id := "tk-" + strconv.Itoa(i)
		summary := issues.IssueSummary{ID: id, Title: "issue " + id, Status: issues.StatusOpen, Type: issues.TypeTask}
		summaries = append(summaries, summary)
		details[id] = issues.IssueDetailView{Issue: issues.Issue{ID: id, Title: "issue " + id, Status: issues.StatusOpen}}
		focused[id] = issues.FocusedGraphView{SelectedID: id, NodeSummaries: map[string]issues.IssueSummary{id: summary}}
	}

	reader := &fakeReader{
		allSummaries: summaries,
		details:      details,
		focused:      focused,
		project:      issues.ProjectGraphView{Issues: summaries},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.width = 120
	m.height = 18
	m.showHelp = true

	rendered := ansiPattern.ReplaceAllString(m.render(), "")
	if got := lipgloss.Height(rendered); got > m.height {
		t.Fatalf("expected render height to stay within the viewport, got %d lines for a %d-line terminal:\n%s", got, m.height, rendered)
	}

	if !strings.Contains(rendered, "q quit") || !strings.Contains(rendered, "Controls") {
		t.Fatalf("expected footer and help to remain visible, got:\n%s", rendered)
	}
}

func TestProjectGraphTabUsesDepthColumnsWhenCentered(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		allSummaries: []issues.IssueSummary{
			{ID: "tk-2", Title: "Focus task", Status: issues.StatusOpen, Type: issues.TypeTask},
		},
		details: map[string]issues.IssueDetailView{
			"tk-2": {Issue: issues.Issue{ID: "tk-2", Title: "Focus task", Status: issues.StatusOpen}},
		},
		focused: map[string]issues.FocusedGraphView{
			"tk-2": {SelectedID: "tk-2", NodeSummaries: map[string]issues.IssueSummary{"tk-2": {ID: "tk-2", Title: "Focus task", Status: issues.StatusOpen, Type: issues.TypeTask}}},
		},
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-1", Title: "Upstream blocker", Status: issues.StatusOpen, Type: issues.TypeBug},
				{ID: "tk-2", Title: "Focus task", Status: issues.StatusOpen, Type: issues.TypeTask},
				{ID: "tk-3", Title: "Downstream child", Status: issues.StatusOpen, Type: issues.TypeTask},
				{ID: "tk-4", Title: "Detached issue", Status: issues.StatusClosed, Type: issues.TypeTask},
			},
			Links: []issues.Link{
				{Kind: "blocks", SourceID: "tk-1", TargetID: "tk-2"},
				{Kind: "parent_child", SourceID: "tk-2", TargetID: "tk-3"},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.focus = paneDetail
	m.activeTab = tabProjectGraph

	rendered := m.renderProjectGraphTab(120, 18)
	if !strings.Contains(rendered, "Depth -1") || !strings.Contains(rendered, "Focus") || !strings.Contains(rendered, "Depth +1") || !strings.Contains(rendered, "Detached") {
		t.Fatalf("expected depth-grouped project graph, got:\n%s", rendered)
	}

	if !strings.Contains(rendered, "tk-1") || !strings.Contains(rendered, "tk-2") || !strings.Contains(rendered, "tk-3") || !strings.Contains(rendered, "tk-4") {
		t.Fatalf("expected all project graph nodes to render, got:\n%s", rendered)
	}
}

func TestProjectGraphTabFallsBackToClusterLayoutWithoutSelection(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{
		project: issues.ProjectGraphView{
			Issues: []issues.IssueSummary{
				{ID: "tk-1", Title: "Root epic", Status: issues.StatusOpen, Type: issues.TypeEpic},
				{ID: "tk-2", Title: "Active task", Status: issues.StatusInProgress, Type: issues.TypeTask, ParentID: "tk-1"},
				{ID: "tk-3", Title: "Ready task", Status: issues.StatusOpen, Type: issues.TypeTask, ParentID: "tk-1"},
				{ID: "tk-4", Title: "Blocked task", Status: issues.StatusBlocked, Type: issues.TypeTask, ParentID: "tk-1"},
				{ID: "tk-5", Title: "Closed task", Status: issues.StatusClosed, Type: issues.TypeTask, ParentID: "tk-1"},
			},
		},
	}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	m.focus = paneDetail
	m.activeTab = tabProjectGraph

	rendered := m.renderProjectGraphTab(160, 18)
	if !strings.Contains(rendered, "Roots") || !strings.Contains(rendered, "In Progress") || !strings.Contains(rendered, "Ready") || !strings.Contains(rendered, "Blocked") || !strings.Contains(rendered, "Closed") {
		t.Fatalf("expected clustered fallback columns, got:\n%s", rendered)
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

func TestCtrlRRefreshPreservesViewportStateForSameDetailTarget(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	first, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "first",
		Description: strings.Repeat("detail line\n", 24),
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

	m.pinnedID = first.ID
	m.focus = paneDetail
	m.activeTab = tabProjectGraph
	m.browserViewport = textViewport{y: 3}
	m.detailsViewport = textViewport{y: 7}
	m.commentsViewport = textViewport{y: 5}
	m.projectGraphViewport = graphViewport{x: 12, y: 8}

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

	if m.browserViewport.y != 3 {
		t.Fatalf("expected browser viewport preserved, got %#v", m.browserViewport)
	}

	if m.detailsViewport.y != 7 {
		t.Fatalf("expected details viewport preserved, got %#v", m.detailsViewport)
	}

	if m.commentsViewport.y != 5 {
		t.Fatalf("expected comments viewport preserved, got %#v", m.commentsViewport)
	}

	if m.projectGraphViewport != (graphViewport{x: 12, y: 8}) {
		t.Fatalf("expected graph viewport preserved, got %#v", m.projectGraphViewport)
	}
}

func TestAutoRefreshReloadsFromDiskAndSchedulesNextTick(t *testing.T) {
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

	m.focus = paneDetail
	m.activeTab = tabProjectGraph
	m.pinnedID = "tk-1"
	m.browserViewport = textViewport{y: 3}
	m.detailsViewport = textViewport{y: 7}
	m.commentsViewport = textViewport{y: 5}
	m.projectGraphViewport = graphViewport{x: 12, y: 8}

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

	updated, cmd := m.Update(autoRefreshMsg{})

	next, ok := updated.(*model)
	if !ok {
		t.Fatalf("expected model update, got %T", updated)
	}

	if cmd == nil {
		t.Fatalf("expected auto-refresh to schedule the next tick")
	}

	if len(next.summaries) != 2 {
		t.Fatalf("expected refreshed summaries from disk, got %#v", next.summaries)
	}

	if next.pinnedID != "tk-1" {
		t.Fatalf("expected pinned issue to survive refresh, got %q", next.pinnedID)
	}

	if next.focus != paneDetail || next.activeTab != tabProjectGraph {
		t.Fatalf("expected focus and tab to survive refresh, got focus=%q tab=%v", next.focus, next.activeTab)
	}

	if next.browserViewport.y != 3 {
		t.Fatalf("expected browser viewport preserved, got %#v", next.browserViewport)
	}

	if next.detailsViewport.y != 7 {
		t.Fatalf("expected details viewport preserved, got %#v", next.detailsViewport)
	}

	if next.commentsViewport.y != 5 {
		t.Fatalf("expected comments viewport preserved, got %#v", next.commentsViewport)
	}

	if next.projectGraphViewport != (graphViewport{x: 12, y: 8}) {
		t.Fatalf("expected graph viewport preserved, got %#v", next.projectGraphViewport)
	}
}

func TestAutoRefreshResetsDetailViewportsWhenPinnedIssueFallsOutOfView(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	first, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "first",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	second, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "second",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	m, err := newModel(ctx, repo, s, StartupOptions{Source: DataSourceReady})
	if err != nil {
		t.Fatal(err)
	}
	defer m.closeReader()

	m.pinnedID = first.ID
	m.focus = paneDetail
	m.activeTab = tabProjectGraph
	m.browserViewport = textViewport{y: 4}
	m.detailsViewport = textViewport{y: 7}
	m.commentsViewport = textViewport{y: 5}
	m.projectGraphViewport = graphViewport{x: 9, y: 6}

	other, err := store.Open(filepath.Join(repo, ".tack", "issues.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeReader(other)

	_, err = other.CloseIssue(ctx, first.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := m.Update(autoRefreshMsg{})

	next, ok := updated.(*model)
	if !ok {
		t.Fatalf("expected model update, got %T", updated)
	}

	if next.pinnedID != "" {
		t.Fatalf("expected pinned issue cleared after refresh, got %q", next.pinnedID)
	}

	if next.currentDetailID() != second.ID {
		t.Fatalf("expected detail view to move to surviving issue, got %q", next.currentDetailID())
	}

	if next.browserViewport.y != 4 {
		t.Fatalf("expected browser viewport preserved, got %#v", next.browserViewport)
	}

	if next.detailsViewport.y != 0 {
		t.Fatalf("expected details viewport reset, got %#v", next.detailsViewport)
	}

	if next.commentsViewport.y != 0 {
		t.Fatalf("expected comments viewport reset, got %#v", next.commentsViewport)
	}

	if next.projectGraphViewport != (graphViewport{}) {
		t.Fatalf("expected graph viewport reset, got %#v", next.projectGraphViewport)
	}
}

func TestCtrlRRefreshResetsDetailViewportsWhenPinnedIssueFallsOutOfView(t *testing.T) {
	ctx := testutil.Context(t)
	repo := testutil.TempRepo(t)
	s := testutil.InitStore(t, repo)

	first, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "first",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	second, err := s.CreateIssue(ctx, store.CreateIssueInput{
		Title:       "second",
		Description: "body",
		Type:        issues.TypeTask,
		Priority:    "medium",
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}

	m, err := newModel(ctx, repo, s, StartupOptions{Source: DataSourceReady})
	if err != nil {
		t.Fatal(err)
	}
	defer m.closeReader()

	m.pinnedID = first.ID
	m.focus = paneDetail
	m.activeTab = tabProjectGraph
	m.browserViewport = textViewport{y: 4}
	m.detailsViewport = textViewport{y: 7}
	m.commentsViewport = textViewport{y: 5}
	m.projectGraphViewport = graphViewport{x: 9, y: 6}

	other, err := store.Open(filepath.Join(repo, ".tack", "issues.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeReader(other)

	_, err = other.CloseIssue(ctx, first.ID, "done", "alice")
	if err != nil {
		t.Fatal(err)
	}

	m.handleKey("ctrl+r")

	if m.pinnedID != "" {
		t.Fatalf("expected pinned issue cleared after refresh, got %q", m.pinnedID)
	}

	if m.currentDetailID() != second.ID {
		t.Fatalf("expected detail view to move to surviving issue, got %q", m.currentDetailID())
	}

	if m.browserViewport.y != 4 {
		t.Fatalf("expected browser viewport preserved, got %#v", m.browserViewport)
	}

	if m.detailsViewport.y != 0 {
		t.Fatalf("expected details viewport reset, got %#v", m.detailsViewport)
	}

	if m.commentsViewport.y != 0 {
		t.Fatalf("expected comments viewport reset, got %#v", m.commentsViewport)
	}

	if m.projectGraphViewport != (graphViewport{}) {
		t.Fatalf("expected graph viewport reset, got %#v", m.projectGraphViewport)
	}
}

func TestExpandedHelpMentionsAutoRefresh(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{project: issues.ProjectGraphView{}}

	m, err := newModel(context.Background(), "/repo", reader, StartupOptions{})
	if err != nil {
		t.Fatal(err)
	}

	help := m.renderExpandedHelp()
	if !strings.Contains(help, "auto-refresh runs every 5 seconds") || !strings.Contains(help, "ctrl+r refreshes immediately from disk") {
		t.Fatalf("expected expanded help to mention auto-refresh, got:\n%s", help)
	}
}

func mustParseTime(t *testing.T, raw string) time.Time {
	t.Helper()

	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parse time %q: %v", raw, err)
	}

	return value
}

type fakeReader struct {
	listCalls  int
	readyCalls int

	listFilters  []store.ListFilter
	readyFilters []store.ListFilter

	filterValueRequests []filterValueRequest

	allSummaries          []issues.IssueSummary
	readySummaries        []issues.IssueSummary
	listByFilter          func(store.ListFilter) []issues.IssueSummary
	readyByFilter         func(store.ListFilter) []issues.IssueSummary
	filterValuesByRequest func(store.FilterValueSource, store.FilterValueKey, store.ListFilter) []string
	details               map[string]issues.IssueDetailView
	focused               map[string]issues.FocusedGraphView
	project               issues.ProjectGraphView
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

func (f *fakeReader) ListFilterValues(_ context.Context, source store.FilterValueSource, key store.FilterValueKey, filter store.ListFilter) ([]string, error) {
	f.filterValueRequests = append(f.filterValueRequests, filterValueRequest{
		source: source,
		key:    key,
		filter: filter,
	})

	if f.filterValuesByRequest != nil {
		return append([]string(nil), f.filterValuesByRequest(source, key, filter)...), nil
	}

	return nil, nil
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

type filterValueRequest struct {
	source store.FilterValueSource
	key    store.FilterValueKey
	filter store.ListFilter
}

func cloneSummaries(in []issues.IssueSummary) []issues.IssueSummary {
	if in == nil {
		return nil
	}

	out := make([]issues.IssueSummary, len(in))
	copy(out, in)

	return out
}
