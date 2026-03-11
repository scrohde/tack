package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"

	"tack/internal/issues"
	"tack/internal/store"
)

type DataSource string

const (
	DataSourceAll   DataSource = "all"
	DataSourceReady DataSource = "ready"
)

type StartupOptions struct {
	Source DataSource
	Filter store.ListFilter
}

type summaryReader interface {
	ListIssueSummaries(context.Context, store.ListFilter) ([]issues.IssueSummary, error)
	ReadyIssueSummaries(context.Context, store.ListFilter) ([]issues.IssueSummary, error)
	IssueDetailView(context.Context, string) (issues.IssueDetailView, error)
	FocusedGraphView(context.Context, string) (issues.FocusedGraphView, error)
	ProjectGraphView(context.Context) (issues.ProjectGraphView, error)
	Close() error
}

type paneFocus string

const (
	paneBrowser paneFocus = "browser"
	paneDetail  paneFocus = "detail"
)

type detailTab int

const (
	tabDetails detailTab = iota
	tabComments
	tabFocusedGraph
	tabProjectGraph
)

var detailTabNames = []string{"Details", "Comments", "Focused Graph", "Project Graph"}

type model struct {
	ctx      context.Context
	repoRoot string
	reader   summaryReader

	source DataSource
	filter store.ListFilter

	summaries []issues.IssueSummary
	selected  int
	pinnedID  string

	focus     paneFocus
	activeTab detailTab
	showHelp  bool

	detailView       issues.IssueDetailView
	focusedGraphView issues.FocusedGraphView
	projectGraphView issues.ProjectGraphView

	width  int
	height int

	lastError error
}

func Run(ctx context.Context, stdout, _ io.Writer, options StartupOptions) error {
	repoRoot, s, err := store.OpenRepo(".")
	if err != nil {
		return err
	}
	defer closeStore(s)

	m, err := newModel(ctx, repoRoot, s, options)
	if err != nil {
		return err
	}

	if stdout == nil {
		return nil
	}

	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(stdout))
	_, err = program.Run()

	return err
}

func newModel(ctx context.Context, repoRoot string, reader summaryReader, options StartupOptions) (*model, error) {
	m := &model{
		ctx:       ctx,
		repoRoot:  repoRoot,
		reader:    reader,
		source:    options.DataSource(),
		filter:    options.Filter,
		focus:     paneBrowser,
		activeTab: tabDetails,
		width:     120,
		height:    34,
	}

	err := m.reload()
	if err != nil {
		return nil, err
	}

	return m, nil
}

func (o StartupOptions) DataSource() DataSource {
	if o.Source == "" {
		return DataSourceAll
	}

	return o.Source
}

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}

		if msg.Height > 0 {
			m.height = msg.Height
		}

		return m, nil
	case tea.KeyPressMsg:
		return m, m.handleKey(msg.String())
	}

	return m, nil
}

func (m *model) handleKey(key string) tea.Cmd {
	switch key {
	case "q":
		return tea.Quit
	case "?":
		m.showHelp = !m.showHelp
	case "tab":
		m.advanceFocusOrTab()
	case "shift+tab":
		m.reverseFocusOrTab()
	case "j", "down":
		m.moveSelection(1)
	case "k", "up":
		m.moveSelection(-1)
	case "enter":
		m.pinSelection()
	case "esc":
		m.focus = paneBrowser
		if m.pinnedID != "" {
			m.pinnedID = ""
			m.syncDetailViews()
		}
	case "r":
		m.toggleSource()
	case "ctrl+r":
		m.refresh()
	case "g":
		m.activeTab = tabFocusedGraph
		m.focus = paneDetail
	case "G":
		m.activeTab = tabProjectGraph
		m.focus = paneDetail
	}

	return nil
}

func (m *model) View() tea.View {
	content := m.render()

	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "tack tui"

	return view
}

func (m *model) render() string {
	if m.width < 60 || m.height < 12 {
		return m.renderCompactWarning()
	}

	header := headerStyle.Render(m.renderHeader())
	footer := footerStyle.Render(m.renderFooter())
	bodyHeight := maxInt(8, m.height-6)

	var body string

	if m.width < 96 {
		topHeight := maxInt(4, bodyHeight/2)
		bottomHeight := maxInt(4, bodyHeight-topHeight)
		left := m.renderBrowserPane(m.width, topHeight)
		right := m.renderDetailPane(m.width, bottomHeight)
		body = lipgloss.JoinVertical(lipgloss.Left, left, right)
	} else {
		leftWidth := maxInt(28, m.width/3)
		rightWidth := maxInt(32, m.width-leftWidth)
		left := m.renderBrowserPane(leftWidth, bodyHeight)
		right := m.renderDetailPane(rightWidth, bodyHeight)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}

	rendered := []string{header, body, footer}

	if m.showHelp {
		rendered = append(rendered, helpStyle.Render(m.renderExpandedHelp()))
	}

	return lipgloss.JoinVertical(lipgloss.Left, rendered...)
}

func (m *model) renderCompactWarning() string {
	lines := []string{
		"Terminal too small for the full tack TUI.",
		fmt.Sprintf("Need about 60x12, have %dx%d.", m.width, m.height),
		fmt.Sprintf("source=%s filter=%s results=%d", m.source, formatFilter(m.filter), len(m.summaries)),
	}

	if summary := m.selectedSummary(); summary != nil {
		lines = append(lines, "selected="+summary.ID+" "+summary.Title)
	}

	return strings.Join(lines, "\n")
}

func (m *model) renderHeader() string {
	selectedID := "-"
	if summary := m.selectedSummary(); summary != nil {
		selectedID = summary.ID
	}

	detailID := m.currentDetailID()
	if detailID == "" {
		detailID = "-"
	}

	parts := []string{
		fmt.Sprintf("repo %s", m.repoRoot),
		fmt.Sprintf("source %s", m.source),
		fmt.Sprintf("filters %s", formatFilter(m.filter)),
		fmt.Sprintf("results %d", len(m.summaries)),
		fmt.Sprintf("selected %s", selectedID),
		fmt.Sprintf("detail %s", detailID),
	}

	if m.pinnedID != "" {
		parts = append(parts, "pinned")
	}

	if m.lastError != nil {
		parts = append(parts, "error "+m.lastError.Error())
	}

	return strings.Join(parts, "  |  ")
}

func (m *model) renderFooter() string {
	return "q quit  tab/shift+tab switch  j/k move  enter pin  esc browser  ? help  r toggle source  ctrl+r refresh  g/G graph tabs"
}

func (m *model) renderExpandedHelp() string {
	return strings.Join([]string{
		"Controls",
		"q quit, ? toggle help, tab/shift+tab switch panes or tabs",
		"j/k and arrows move the issue selection",
		"enter pins the selected issue in the detail pane",
		"esc returns focus to the browser and clears the current pin",
		"r toggles between all issues and ready issues, ctrl+r refreshes from disk",
		"g opens Focused Graph, G opens Project Graph",
	}, "\n")
}

func (m *model) renderBrowserPane(width, height int) string {
	rows := []string{}

	if len(m.summaries) == 0 {
		rows = append(rows, "No matching issues.")
	} else {
		for i, summary := range m.summaries {
			marker := " "
			if i == m.selected {
				marker = ">"
			}

			pin := " "
			if summary.ID == m.currentDetailID() {
				pin = "*"
			}

			line := fmt.Sprintf("%s%s %-6s %-11s %-8s %s", marker, pin, summary.ID, summary.Status, summary.Type, summary.Title)
			rows = append(rows, styleIssueLine(summary, i == m.selected, summary.ID == m.currentDetailID()).Render(line))
		}
	}

	body := strings.Join(rows, "\n")

	title := "Issue Browser"
	if m.focus == paneBrowser {
		title += " [active]"
	}

	return paneStyle(width, height, m.focus == paneBrowser).Render(title + "\n\n" + body)
}

func (m *model) renderDetailPane(width, height int) string {
	tabParts := make([]string, 0, len(detailTabNames))
	for i, name := range detailTabNames {
		style := inactiveTabStyle
		if detailTab(i) == m.activeTab {
			style = activeTabStyle
		}

		tabParts = append(tabParts, style.Render(name))
	}

	body := strings.Join([]string{
		lipgloss.JoinHorizontal(lipgloss.Top, tabParts...),
		"",
		m.renderActiveTabBody(),
	}, "\n")

	title := "Details"
	if m.focus == paneDetail {
		title += " [active]"
	}

	return paneStyle(width, height, m.focus == paneDetail).Render(title + "\n\n" + body)
}

func (m *model) renderActiveTabBody() string {
	switch m.activeTab {
	case tabComments:
		return m.renderCommentsTab()
	case tabFocusedGraph:
		return m.renderFocusedGraphTab()
	case tabProjectGraph:
		return m.renderProjectGraphTab()
	default:
		return m.renderDetailsTab()
	}
}

func (m *model) renderDetailsTab() string {
	if m.currentDetailID() == "" {
		return "Select an issue to inspect."
	}

	issue := m.detailView.Issue
	lines := []string{
		fmt.Sprintf("%s  %s", issue.ID, issue.Title),
		fmt.Sprintf("status=%s  type=%s  priority=%s  assignee=%s", issue.Status, issue.Type, issue.Priority, blankIfEmpty(issue.Assignee)),
		fmt.Sprintf("labels=%s", formatLabels(issue.Labels)),
		fmt.Sprintf("parent=%s", blankIfEmpty(issue.ParentID)),
		fmt.Sprintf("blocked_by=%s", formatLinkIDs(m.detailView.Dependencies.BlockedBy, func(link issues.Link) string { return link.SourceID })),
		fmt.Sprintf("blocks=%s", formatLinkIDs(m.detailView.Dependencies.Blocks, func(link issues.Link) string { return link.TargetID })),
		"",
		issue.Description,
	}

	return strings.Join(lines, "\n")
}

func (m *model) renderCommentsTab() string {
	if m.currentDetailID() == "" {
		return "Select an issue to inspect."
	}

	if len(m.detailView.Comments) == 0 {
		return "No comments."
	}

	lines := []string{}
	for _, comment := range m.detailView.Comments {
		lines = append(lines, fmt.Sprintf("%s  %s", comment.Author, comment.CreatedAt.Format("2006-01-02 15:04")))
		lines = append(lines, comment.Body)
		lines = append(lines, "")
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (m *model) renderFocusedGraphTab() string {
	if m.currentDetailID() == "" {
		return "Select an issue to inspect."
	}

	lines := []string{
		"Parent",
		m.renderFocusedSummary(m.focusedGraphView.ParentID),
		"",
		"Selected",
		m.renderFocusedSummary(m.focusedGraphView.SelectedID),
		"",
		"Blocked By",
		renderFocusedGroup(m.focusedGraphView.BlockedByIDs, m.focusedGraphView.NodeSummaries),
		"",
		"Blocks",
		renderFocusedGroup(m.focusedGraphView.BlocksIDs, m.focusedGraphView.NodeSummaries),
		"",
		"Children",
		renderFocusedGroup(m.focusedGraphView.ChildIDs, m.focusedGraphView.NodeSummaries),
	}

	return strings.Join(lines, "\n")
}

func (m *model) renderFocusedSummary(id string) string {
	if id == "" {
		return "(none)"
	}

	summary, ok := m.focusedGraphView.NodeSummaries[id]
	if !ok {
		return id
	}

	return summaryLine(summary)
}

func (m *model) renderProjectGraphTab() string {
	if len(m.projectGraphView.Issues) == 0 {
		return "No project graph data."
	}

	lines := []string{"Issues"}

	for _, summary := range m.projectGraphView.Issues {
		prefix := " "
		if summary.ID == m.currentDetailID() {
			prefix = "*"
		}

		lines = append(lines, prefix+" "+summaryLine(summary))
	}

	lines = append(lines, "", "Links")
	for _, link := range m.projectGraphView.Links {
		lines = append(lines, fmt.Sprintf("%s %s -> %s", link.Kind, link.SourceID, link.TargetID))
	}

	return strings.Join(lines, "\n")
}

func (m *model) advanceFocusOrTab() {
	if m.focus == paneBrowser {
		m.focus = paneDetail
		return
	}

	if m.activeTab == tabProjectGraph {
		m.activeTab = tabDetails
		return
	}

	m.activeTab++
}

func (m *model) reverseFocusOrTab() {
	if m.focus == paneBrowser {
		m.focus = paneDetail
		m.activeTab = tabProjectGraph

		return
	}

	if m.activeTab == tabDetails {
		m.focus = paneBrowser
		return
	}

	m.activeTab--
}

func (m *model) moveSelection(delta int) {
	if len(m.summaries) == 0 {
		return
	}

	next := clampInt(m.selected+delta, 0, len(m.summaries)-1)
	if next == m.selected {
		return
	}

	m.selected = next

	if m.pinnedID == "" {
		m.syncDetailViews()
	}
}

func (m *model) pinSelection() {
	summary := m.selectedSummary()
	if summary == nil {
		return
	}

	m.pinnedID = summary.ID
	m.focus = paneDetail
	m.syncDetailViews()
}

func (m *model) toggleSource() {
	if m.source == DataSourceReady {
		m.source = DataSourceAll
	} else {
		m.source = DataSourceReady
	}

	m.refresh()
}

func (m *model) refresh() {
	err := m.reload()
	if err != nil {
		m.lastError = err
	}
}

func (m *model) reload() error {
	summaries, err := m.loadSummaries()
	if err != nil {
		return err
	}

	m.summaries = summaries
	if len(m.summaries) == 0 {
		m.selected = 0
		m.pinnedID = ""
		m.detailView = issues.IssueDetailView{}
		m.focusedGraphView = issues.FocusedGraphView{}
	} else {
		if m.selected >= len(m.summaries) {
			m.selected = len(m.summaries) - 1
		}

		if m.selected < 0 {
			m.selected = 0
		}

		if m.pinnedID != "" && !m.hasSummary(m.pinnedID) {
			m.pinnedID = ""
		}

		m.syncDetailViews()
	}

	project, err := m.reader.ProjectGraphView(m.ctx)
	if err != nil {
		return err
	}

	m.projectGraphView = project
	m.lastError = nil

	return nil
}

func (m *model) loadSummaries() ([]issues.IssueSummary, error) {
	switch m.source {
	case DataSourceReady:
		return m.reader.ReadyIssueSummaries(m.ctx, m.filter)
	default:
		return m.reader.ListIssueSummaries(m.ctx, m.filter)
	}
}

func (m *model) syncDetailViews() {
	id := m.currentDetailID()
	if id == "" {
		m.detailView = issues.IssueDetailView{}
		m.focusedGraphView = issues.FocusedGraphView{}

		return
	}

	detailView, err := m.reader.IssueDetailView(m.ctx, id)
	if err != nil {
		m.lastError = err
		return
	}

	focusedGraphView, err := m.reader.FocusedGraphView(m.ctx, id)
	if err != nil {
		m.lastError = err
		return
	}

	m.detailView = detailView
	m.focusedGraphView = focusedGraphView
	m.lastError = nil
}

func (m *model) currentDetailID() string {
	if m.pinnedID != "" {
		return m.pinnedID
	}

	summary := m.selectedSummary()
	if summary == nil {
		return ""
	}

	return summary.ID
}

func (m *model) selectedSummary() *issues.IssueSummary {
	if len(m.summaries) == 0 || m.selected < 0 || m.selected >= len(m.summaries) {
		return nil
	}

	return &m.summaries[m.selected]
}

func (m *model) hasSummary(id string) bool {
	for _, summary := range m.summaries {
		if summary.ID == id {
			return true
		}
	}

	return false
}

func renderFocusedGroup(ids []string, summaries map[string]issues.IssueSummary) string {
	if len(ids) == 0 {
		return "(none)"
	}

	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		summary, ok := summaries[id]
		if !ok {
			lines = append(lines, id)
			continue
		}

		lines = append(lines, summaryLine(summary))
	}

	return strings.Join(lines, "\n")
}

func summaryLine(summary issues.IssueSummary) string {
	return fmt.Sprintf("%s  [%s] %s", summary.ID, summary.Status, summary.Title)
}

func formatFilter(filter store.ListFilter) string {
	parts := []string{}

	if filter.Status != "" {
		parts = append(parts, "status="+filter.Status)
	}

	if filter.Type != "" {
		parts = append(parts, "type="+filter.Type)
	}

	if filter.Label != "" {
		parts = append(parts, "label="+filter.Label)
	}

	if filter.Assignee != "" {
		parts = append(parts, "assignee="+filter.Assignee)
	}

	if filter.Limit > 0 {
		parts = append(parts, fmt.Sprintf("limit=%d", filter.Limit))
	}

	if len(parts) == 0 {
		return "(none)"
	}

	return strings.Join(parts, " ")
}

func formatLabels(labels []string) string {
	if len(labels) == 0 {
		return "(none)"
	}

	return strings.Join(labels, ", ")
}

func formatLinkIDs[T any](links []T, id func(T) string) string {
	if len(links) == 0 {
		return "(none)"
	}

	out := make([]string, 0, len(links))
	for _, link := range links {
		out = append(out, id(link))
	}

	return strings.Join(out, ", ")
}

func blankIfEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}

	return value
}

func styleIssueLine(summary issues.IssueSummary, selected, pinned bool) lipgloss.Style {
	style := lipgloss.NewStyle()

	if summary.Status == issues.StatusClosed {
		style = style.Foreground(lipgloss.Color("8"))
	}

	if selected {
		style = style.Bold(true)
	}

	if pinned {
		style = style.Foreground(lipgloss.Color("12"))
	}

	return style
}

func paneStyle(width, height int, focused bool) lipgloss.Style {
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		BorderStyle(lipgloss.RoundedBorder()).
		Padding(0, 1)

	if focused {
		return style.BorderForeground(lipgloss.Color("12"))
	}

	return style.BorderForeground(lipgloss.Color("8"))
}

func closeStore(s *store.Store) {
	err := s.Close()
	if err != nil {
		return
	}
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}

	if value > high {
		return high
	}

	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Padding(0, 1)
	helpStyle   = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("8")).
			Padding(0, 1)
	activeTabStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	inactiveTabStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)
