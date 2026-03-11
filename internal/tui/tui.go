package tui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/glamour/v2"
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

	filterInputActive bool
	filterInput       string

	detailView       issues.IssueDetailView
	focusedGraphView issues.FocusedGraphView
	projectGraphView issues.ProjectGraphView

	focusedGraphViewport graphViewport
	projectGraphViewport graphViewport

	width  int
	height int

	descriptionCache markdownRenderCache

	lastError error
}

type markdownRenderCache struct {
	issueID  string
	width    int
	source   string
	rendered string
}

func Run(ctx context.Context, stdout, _ io.Writer, options StartupOptions) error {
	repoRoot, s, err := store.OpenRepo(".")
	if err != nil {
		return err
	}

	m, err := newModel(ctx, repoRoot, s, options)
	if err != nil {
		closeReader(s)
		return err
	}
	defer m.closeReader()

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
	if m.filterInputActive {
		return m.handleFilterInputKey(key)
	}

	if m.handleGraphViewportKey(key) {
		return nil
	}

	switch key {
	case "q":
		return tea.Quit
	case "?":
		m.showHelp = !m.showHelp
	case "/":
		m.filterInputActive = true
		m.filterInput = ""
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
	header := headerStyle.Render(m.renderHeader())
	footer := footerStyle.Render(m.renderFooter())
	parts := []string{header}

	if m.filterInputActive {
		parts = append(parts, filterStyle.Render(m.renderFilterEditor()))
	}

	if m.width < 60 || m.height < 12 {
		parts = append(parts, m.renderCompactLayout(), footer)

		if m.showHelp {
			parts = append(parts, helpStyle.Render(m.renderExpandedHelp()))
		}

		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

	bodyHeight := maxInt(8, m.height-6-len(parts)+1)

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

	rendered := append(parts, body, footer)

	if m.showHelp {
		rendered = append(rendered, helpStyle.Render(m.renderExpandedHelp()))
	}

	return lipgloss.JoinVertical(lipgloss.Left, rendered...)
}

func (m *model) renderCompactLayout() string {
	sections := []string{
		compactWarningStyle.Render(fmt.Sprintf("Compact single-column layout (%dx%d). Full layout needs about 60x12.", m.width, m.height)),
		"",
		compactSectionStyle.Render("Issues"),
		m.renderBrowserBody(maxInt(24, m.width-2)),
		"",
		compactSectionStyle.Render(detailTabNames[m.activeTab]),
		m.renderActiveTabBody(maxInt(24, m.width-2), maxInt(6, m.height/2)),
	}

	return strings.Join(sections, "\n")
}

func (m *model) renderHeader() string {
	repoName := filepath.Base(m.repoRoot)
	if repoName == "." || repoName == string(filepath.Separator) || repoName == "" {
		repoName = m.repoRoot
	}

	selectedID := "-"
	if summary := m.selectedSummary(); summary != nil {
		selectedID = summary.ID
	}

	detailID := m.currentDetailID()
	if detailID == "" {
		detailID = "-"
	}

	parts := []string{fmt.Sprintf("repo %s", repoName), fmt.Sprintf("view %s", m.source)}

	filterSummary := formatFilter(m.filter)
	if filterSummary == "(none)" {
		parts = append(parts, "filters none")
	} else {
		parts = append(parts, "filter "+filterSummary)
	}

	parts = append(parts, fmt.Sprintf("%d issues", len(m.summaries)))

	if selectedID != "-" {
		parts = append(parts, "selected "+selectedID)
	}

	if detailID != "-" {
		parts = append(parts, "detail "+detailID)
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
	return "q quit  tab switch  j/k move  enter pin  / filter  g/G graphs  ? more"
}

func (m *model) renderExpandedHelp() string {
	return strings.Join([]string{
		"Controls",
		"q quit, ? toggle help, tab/shift+tab switch panes or tabs",
		"j/k and arrows move the issue selection",
		"/ opens filter editing; use status=, type=, label=, assignee=, limit=, or reset",
		"enter pins the selected issue in the detail pane",
		"esc returns focus to the browser and clears the current pin",
		"r toggles between all issues and ready issues, ctrl+r refreshes from disk",
		"g opens Focused Graph, G opens Project Graph",
		"In graph tabs with detail focus, h/j/k/l and arrow keys pan the viewport",
	}, "\n")
}

func (m *model) renderBrowserPane(width, height int) string {
	title := fmt.Sprintf("Issue Browser (%d)", len(m.summaries))
	if m.focus == paneBrowser {
		title += " [active]"
	}

	return paneStyle(width, height, m.focus == paneBrowser).Render(paneTitleStyle.Render(title) + "\n\n" + m.renderBrowserBody(maxInt(20, width-4)))
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
		lipgloss.JoinHorizontal(lipgloss.Bottom, tabParts...),
		"",
		m.renderActiveTabBody(maxInt(16, width-4), maxInt(4, height-6)),
	}, "\n")

	title := "Details"
	if detailID := m.currentDetailID(); detailID != "" {
		title += " " + detailID
	}

	if m.focus == paneDetail {
		title += " [active]"
	}

	return paneStyle(width, height, m.focus == paneDetail).Render(paneTitleStyle.Render(title) + "\n\n" + body)
}

func (m *model) renderActiveTabBody(width, height int) string {
	switch m.activeTab {
	case tabComments:
		return m.renderCommentsTab()
	case tabFocusedGraph:
		return m.renderFocusedGraphTab(width, height)
	case tabProjectGraph:
		return m.renderProjectGraphTab(width, height)
	default:
		return m.renderDetailsTab(width)
	}
}

func (m *model) renderDetailsTab(width int) string {
	if m.currentDetailID() == "" {
		return m.renderEmptyDetailState()
	}

	issue := m.detailView.Issue
	lines := []string{
		detailTitleStyle.Render(issue.Title),
		metaLine("id", issue.ID),
		metaLine("status", issue.Status),
		metaLine("type", issue.Type),
		metaLine("priority", blankIfEmpty(issue.Priority)),
		metaLine("assignee", blankIfEmpty(issue.Assignee)),
		metaLine("labels", formatLabels(issue.Labels)),
		"",
		sectionTitleStyle.Render("Relationships"),
		metaLine("parent", m.renderRelatedSummary(issue.ParentID)),
		"",
		sectionTitleStyle.Render("Blocked By"),
		m.renderRelatedLinks(m.detailView.Dependencies.BlockedBy, func(link issues.Link) string { return link.SourceID }),
		"",
		sectionTitleStyle.Render("Blocks"),
		m.renderRelatedLinks(m.detailView.Dependencies.Blocks, func(link issues.Link) string { return link.TargetID }),
	}

	if strings.TrimSpace(issue.Description) != "" {
		lines = append(lines, "", sectionTitleStyle.Render("Description"), m.renderMarkdown(issue.ID, width, issue.Description))
	}

	return strings.Join(lines, "\n")
}

func (m *model) renderCommentsTab() string {
	if m.currentDetailID() == "" {
		return m.renderEmptyDetailState()
	}

	lines := []string{
		detailTitleStyle.Render(m.detailView.Issue.Title),
		metaLine("issue", m.detailView.Issue.ID),
		metaLine("comments", fmt.Sprintf("%d comment(s)", len(m.detailView.Comments))),
	}

	if len(m.detailView.Comments) == 0 {
		lines = append(lines, "", mutedTextStyle.Render("No comments yet."))
		return strings.Join(lines, "\n")
	}

	for _, comment := range m.detailView.Comments {
		lines = append(lines, "")
		lines = append(lines, sectionTitleStyle.Render(comment.Author))
		lines = append(lines, mutedTextStyle.Render(comment.CreatedAt.Format("2006-01-02 15:04")))
		lines = append(lines, comment.Body)
	}

	return strings.Join(lines, "\n")
}

func (m *model) renderFocusedGraphTab(width, height int) string {
	if m.currentDetailID() == "" {
		return "Select an issue to inspect."
	}

	content := renderFocusedGraph(m.focusedGraphView)

	return m.renderGraphViewport(width, height, &m.focusedGraphViewport, content)
}

func (m *model) renderProjectGraphTab(width, height int) string {
	if len(m.projectGraphView.Issues) == 0 {
		return "No project graph data."
	}

	content := renderProjectGraph(m.projectGraphView, m.currentDetailID())

	return m.renderGraphViewport(width, height, &m.projectGraphViewport, content)
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
	err := m.reopenReader()
	if err != nil {
		m.lastError = err
		return
	}

	err = m.reload()
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
		m.resetGraphViewport(tabFocusedGraph)
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
	m.resetGraphViewport(tabProjectGraph)
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
		m.resetGraphViewport(tabFocusedGraph)
		m.resetGraphViewport(tabProjectGraph)

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
	m.resetGraphViewport(tabFocusedGraph)
	m.resetGraphViewport(tabProjectGraph)
	m.lastError = nil
}

func (m *model) handleFilterInputKey(key string) tea.Cmd {
	switch key {
	case "esc":
		m.filterInputActive = false
		m.filterInput = ""
	case "enter":
		next, err := parseFilterInput(m.filter, m.filterInput)
		if err != nil {
			m.lastError = err
			return nil
		}

		m.filter = next
		m.filterInputActive = false
		m.filterInput = ""

		err = m.reload()
		if err != nil {
			m.lastError = err
		}
	case "backspace", "ctrl+h":
		if len(m.filterInput) > 0 {
			m.filterInput = m.filterInput[:len(m.filterInput)-1]
		}
	case "space":
		m.filterInput += " "
	default:
		if len(key) == 1 {
			m.filterInput += key
		}
	}

	return nil
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

func (m *model) renderFilterEditor() string {
	lines := []string{
		sectionTitleStyle.Render("Filter Editor"),
		"Current: " + formatFilter(m.filter),
		"Use key=value tokens (status, type, label, assignee, limit) or reset.",
		"> " + m.filterInput,
	}

	return strings.Join(lines, "\n")
}

func (m *model) renderBrowserBody(width int) string {
	if m.isEmptyRepo() {
		return strings.Join([]string{
			"No issues yet.",
			"Create one with `tack create --title \"...\" --type task --priority medium`.",
			"Or import a plan with `tack import --file plan.json`.",
		}, "\n")
	}

	if len(m.summaries) == 0 {
		return strings.Join([]string{
			"No matching issues.",
			fmt.Sprintf("Current filters: %s", formatFilter(m.filter)),
			"Press / to adjust filters or r to toggle ready/all.",
		}, "\n")
	}

	indicatorWidth := 2
	idWidth := 6
	statusWidth := 11
	typeWidth := 8
	titleWidth := maxInt(12, width-(indicatorWidth+idWidth+statusWidth+typeWidth+4))

	rows := make([]string, 0, len(m.summaries)+1)
	rows = append(rows, tableHeaderStyle.Render(fmt.Sprintf("%-*s %-*s %-*s %-*s %s", indicatorWidth, "", idWidth, "ID", statusWidth, "STATUS", typeWidth, "TYPE", "TITLE")))

	for i, summary := range m.summaries {
		marker := "  "
		if i == m.selected {
			marker = "> "
		} else if summary.ID == m.currentDetailID() {
			marker = "* "
		}

		line := fmt.Sprintf("%-*s %-*s %-*s %-*s %s", indicatorWidth, marker, idWidth, summary.ID, statusWidth, summary.Status, typeWidth, summary.Type, truncateText(summary.Title, titleWidth))
		rows = append(rows, styleIssueLine(summary, i == m.selected, summary.ID == m.currentDetailID()).Render(line))
	}

	return strings.Join(rows, "\n")
}

func (m *model) renderEmptyDetailState() string {
	if m.isEmptyRepo() {
		return strings.Join([]string{
			"Start by creating or importing issues.",
			"`tack create` adds a single issue.",
			"`tack import --file plan.json` imports a linked plan.",
		}, "\n")
	}

	if len(m.summaries) == 0 {
		return "No matching issues for the current filters."
	}

	return "Select an issue to inspect."
}

func (m *model) isEmptyRepo() bool {
	return len(m.summaries) == 0 && len(m.projectGraphView.Issues) == 0
}

func (m *model) renderRelatedSummary(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "(none)"
	}

	summary, ok := m.detailView.RelatedSummaries[id]
	if !ok {
		return id
	}

	return summaryLine(summary)
}

func (m *model) renderRelatedLinks(links []issues.Link, id func(issues.Link) string) string {
	if len(links) == 0 {
		return "(none)"
	}

	lines := make([]string, 0, len(links))
	for _, link := range links {
		lines = append(lines, m.renderRelatedSummary(id(link)))
	}

	return strings.Join(lines, "\n")
}

func (m *model) reopenReader() error {
	storeReader, ok := m.reader.(*store.Store)
	if !ok {
		return nil
	}

	repoRoot, reopened, err := store.OpenRepo(m.repoRoot)
	if err != nil {
		return err
	}

	m.repoRoot = repoRoot
	m.reader = reopened

	closeReader(storeReader)

	return nil
}

func (m *model) closeReader() {
	closeReader(m.reader)
}

func summaryLine(summary issues.IssueSummary) string {
	return fmt.Sprintf("%s  [%s] %s", summary.ID, summary.Status, summary.Title)
}

func (m *model) renderMarkdown(issueID string, width int, source string) string {
	width = maxInt(20, width)

	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}

	if m.descriptionCache.issueID == issueID && m.descriptionCache.width == width && m.descriptionCache.source == source {
		return m.descriptionCache.rendered
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return source
	}

	rendered, err := renderer.Render(source)
	if err != nil {
		return source
	}

	rendered = strings.TrimRight(rendered, "\n")
	if strings.TrimSpace(rendered) == "" {
		return source
	}

	m.descriptionCache = markdownRenderCache{
		issueID:  issueID,
		width:    width,
		source:   source,
		rendered: rendered,
	}

	return rendered
}

func metaLine(label, value string) string {
	return fmt.Sprintf("%-9s %s", label, value)
}

func parseFilterInput(current store.ListFilter, input string) (store.ListFilter, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return current, nil
	}

	if trimmed == "reset" {
		return store.ListFilter{}, nil
	}

	next := current

	for _, token := range strings.Fields(trimmed) {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			return store.ListFilter{}, fmt.Errorf("invalid filter token %q", token)
		}

		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)

		switch key {
		case "status":
			if value != "" && !issues.IsValidStatus(value) {
				return store.ListFilter{}, fmt.Errorf("invalid status %q", value)
			}

			next.Status = value
		case "type":
			if value != "" && !issues.IsValidType(value) {
				return store.ListFilter{}, fmt.Errorf("invalid type %q", value)
			}

			next.Type = value
		case "label":
			next.Label = value
		case "assignee":
			next.Assignee = value
		case "limit":
			if value == "" {
				next.Limit = 0
				continue
			}

			limit, err := strconv.Atoi(value)
			if err != nil || limit < 0 {
				return store.ListFilter{}, fmt.Errorf("invalid limit %q", value)
			}

			next.Limit = limit
		default:
			return store.ListFilter{}, fmt.Errorf("unknown filter key %q", key)
		}
	}

	return next, nil
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

func blankIfEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}

	return value
}

func styleIssueLine(summary issues.IssueSummary, selected, pinned bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("12"))
	}

	if pinned {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("12"))
	}

	style := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	if summary.Status == issues.StatusClosed {
		style = style.Foreground(lipgloss.Color("8"))
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

	return style.BorderForeground(lipgloss.Color("7"))
}

func closeReader(reader summaryReader) {
	if reader == nil {
		return
	}

	err := reader.Close()
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
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("235")).
			Padding(0, 1)
	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Background(lipgloss.Color("235")).
			Padding(0, 1)
	helpStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("8")).
			Padding(0, 1)
	filterStyle         = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("6")).Padding(0, 1)
	compactWarningStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	compactSectionStyle = lipgloss.NewStyle().Bold(true)
	activeTabStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("12")).Padding(0, 1).MarginRight(1)
	inactiveTabStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("237")).Padding(0, 1).MarginRight(1)
	paneTitleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	tableHeaderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	detailTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	sectionTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	mutedTextStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func truncateText(text string, width int) string {
	if width <= 0 {
		return ""
	}

	runes := []rune(text)
	if len(runes) <= width {
		return text
	}

	if width == 1 {
		return "…"
	}

	return string(runes[:width-1]) + "…"
}
