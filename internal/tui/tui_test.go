package tui

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

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
		t.Fatalf("expected comments tab, got %d", m.activeTab)
	}

	m.handleKey("right")

	if m.activeTab != tabProjectGraph {
		t.Fatalf("expected project graph tab, got %d", m.activeTab)
	}

	m.handleKey("left")

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
	if !strings.Contains(footer, "tab/left/right switch") {
		t.Fatalf("expected footer to mention left/right switching, got %q", footer)
	}

	help := m.renderExpandedHelp()
	if !strings.Contains(help, "tab/shift+tab and left/right switch panes or tabs") {
		t.Fatalf("expected help to mention left/right navigation, got:\n%s", help)
	}

	if !strings.Contains(help, "g or G opens Project Graph") {
		t.Fatalf("expected help to mention the single graph tab, got:\n%s", help)
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

func TestFilterEditorUpdatesFilterAndSummaries(t *testing.T) {
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

	for _, ch := range "status=blocked" {
		m.handleKey(string(ch))
	}

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
					Description: "# Context\n\n- detail body\n- next step",
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

	comments := m.renderCommentsTab()
	if !strings.Contains(comments, "1 comment(s)") || !strings.Contains(comments, "needs follow-up") {
		t.Fatalf("comments tab did not render typed comments:\n%s", comments)
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
