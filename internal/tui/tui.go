package tui

import (
	"context"
	"errors"
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
	tabProjectGraph
)

var detailTabNames = []string{"Details", "Comments", "Project Graph"}

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
	projectGraphView issues.ProjectGraphView

	browserViewport      textViewport
	detailsViewport      textViewport
	commentsViewport     textViewport
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

type textViewport struct {
	y int
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

	if m.handleTextViewportKey(key) {
		return nil
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
	case "tab", "right":
		m.advanceFocusOrTab()
	case "shift+tab", "left":
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
		m.activeTab = tabProjectGraph
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

	help := ""
	if m.showHelp {
		help = helpStyle.Render(m.renderExpandedHelp())
	}

	parts := []string{header}

	if m.filterInputActive {
		parts = append(parts, filterStyle.Render(m.renderFilterEditor()))
	}

	staticHeight := renderedHeight(parts...) + lipgloss.Height(footer)
	if help != "" {
		staticHeight += lipgloss.Height(help)
	}

	bodyHeight := maxInt(1, m.height-staticHeight)

	if m.width < 60 || m.height < 12 || bodyHeight < 8 {
		parts = append(parts, m.renderCompactLayout(bodyHeight), footer)

		if help != "" {
			parts = append(parts, help)
		}

		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

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

	if help != "" {
		rendered = append(rendered, help)
	}

	return lipgloss.JoinVertical(lipgloss.Left, rendered...)
}

func (m *model) renderCompactLayout(height int) string {
	warning := compactWarningStyle.Render(fmt.Sprintf("Compact single-column layout (%dx%d). Full layout needs about 60x12.", m.width, m.height))
	width := maxInt(24, m.width-2)

	switch {
	case height <= 1:
		return warning
	case height == 2:
		return strings.Join([]string{
			warning,
			m.renderBrowserBody(width, 1),
		}, "\n")
	case height == 3:
		return strings.Join([]string{
			warning,
			compactSectionStyle.Render("Issues"),
			m.renderBrowserBody(width, 1),
		}, "\n")
	case height == 4:
		return strings.Join([]string{
			warning,
			compactSectionStyle.Render("Issues"),
			m.renderBrowserBody(width, 1),
			compactSectionStyle.Render(detailTabNames[m.activeTab]),
		}, "\n")
	default:
		contentHeight := height - 3
		browserHeight := maxInt(1, contentHeight/2)
		detailHeight := maxInt(1, contentHeight-browserHeight)

		return strings.Join([]string{
			warning,
			compactSectionStyle.Render("Issues"),
			m.renderBrowserBody(width, browserHeight),
			compactSectionStyle.Render(detailTabNames[m.activeTab]),
			m.renderActiveTabBody(width, detailHeight),
		}, "\n")
	}
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
	return "q quit  tab/left/right switch  j/k move or scroll  enter pin  / filter  g/G graph  ? more"
}

func (m *model) renderExpandedHelp() string {
	return strings.Join([]string{
		"Controls",
		"q quit, ? toggle help, tab/shift+tab and left/right switch panes or tabs",
		"j/k and up/down move the issue selection",
		"/ opens filter editing; use status=, type=, label=, assignee=, limit=, or reset",
		"enter pins the selected issue in the detail pane",
		"esc returns focus to the browser and clears the current pin",
		"r toggles between all issues and ready issues, ctrl+r refreshes from disk",
		"g or G opens Project Graph",
		"In Details and Comments with detail focus, j/k, up/down, pgup/pgdown, and ctrl+u/ctrl+d scroll",
		"In the graph tab with detail focus, h/l pan horizontally while j/k and up/down pan vertically",
	}, "\n")
}

func (m *model) renderBrowserPane(width, height int) string {
	title := fmt.Sprintf("Issue Browser (%d)", len(m.summaries))
	if m.focus == paneBrowser {
		title += " [active]"
	}

	return paneStyle(width, height, m.focus == paneBrowser).Render(paneTitleStyle.Render(title) + "\n\n" + m.renderBrowserBody(maxInt(20, width-4), maxInt(1, height-4)))
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
		return m.renderTextViewport(&m.commentsViewport, height, m.renderCommentsTab())
	case tabProjectGraph:
		return m.renderProjectGraphTab(width, height)
	default:
		return m.renderTextViewport(&m.detailsViewport, height, m.renderDetailsTab(width))
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
	}

	if m.detailView.LatestCloseReason != "" {
		lines = append(lines, metaLine("close reason", m.detailView.LatestCloseReason))
	}

	if m.detailView.LatestReopenReason != "" {
		lines = append(lines, metaLine("reopen reason", m.detailView.LatestReopenReason))
	}

	lines = append(lines,
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
	)

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
		m.resetTextViewport(tabDetails)
		m.resetTextViewport(tabComments)
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
		m.resetTextViewport(tabDetails)
		m.resetTextViewport(tabComments)
		m.resetGraphViewport(tabProjectGraph)

		return
	}

	detailView, err := m.reader.IssueDetailView(m.ctx, id)
	if err != nil {
		m.lastError = err
		return
	}

	m.detailView = detailView
	m.resetTextViewport(tabDetails)
	m.resetTextViewport(tabComments)
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

func (m *model) renderBrowserBody(width, height int) string {
	if height <= 0 {
		return ""
	}

	if m.isEmptyRepo() {
		return m.renderTextViewport(&m.browserViewport, maxInt(1, height), strings.Join([]string{
			"No issues yet.",
			"Create one with `tack create --title \"...\" --type task --priority medium`.",
			"Or import a plan with `tack import --file plan.json`.",
		}, "\n"))
	}

	if len(m.summaries) == 0 {
		return m.renderTextViewport(&m.browserViewport, maxInt(1, height), strings.Join([]string{
			"No matching issues.",
			fmt.Sprintf("Current filters: %s", formatFilter(m.filter)),
			"Press / to adjust filters or r to toggle ready/all.",
		}, "\n"))
	}

	indicatorWidth := 2
	idWidth := 6
	statusWidth := 11
	typeWidth := 8
	titleWidth := maxInt(12, width-(indicatorWidth+idWidth+statusWidth+typeWidth+4))

	header := tableHeaderStyle.Render(fmt.Sprintf("%-*s %-*s %-*s %-*s %s", indicatorWidth, "", idWidth, "ID", statusWidth, "STATUS", typeWidth, "TYPE", "TITLE"))
	if height == 1 {
		return header
	}

	visibleRows := maxInt(1, height-1)

	maxY := maxInt(0, len(m.summaries)-visibleRows)
	if m.selected < m.browserViewport.y {
		m.browserViewport.y = m.selected
	}

	if m.selected >= m.browserViewport.y+visibleRows {
		m.browserViewport.y = m.selected - visibleRows + 1
	}

	m.browserViewport.y = clampInt(m.browserViewport.y, 0, maxY)

	end := clampInt(m.browserViewport.y+visibleRows, m.browserViewport.y, len(m.summaries))

	rows := make([]string, 0, visibleRows+1)
	rows = append(rows, header)

	for i := m.browserViewport.y; i < end; i++ {
		summary := m.summaries[i]

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

func (m *model) handleTextViewportKey(key string) bool {
	if m.focus != paneDetail {
		return false
	}

	viewport := m.activeTextViewport()
	if viewport == nil {
		return false
	}

	switch key {
	case "up", "k":
		viewport.y = maxInt(0, viewport.y-1)
		return true
	case "down", "j":
		viewport.y++
		return true
	case "pgup", "ctrl+u":
		viewport.y = maxInt(0, viewport.y-8)
		return true
	case "pgdown", "ctrl+d":
		viewport.y += 8
		return true
	case "home":
		viewport.y = 0
		return true
	default:
		return false
	}
}

func (m *model) activeTextViewport() *textViewport {
	switch m.activeTab {
	case tabDetails:
		return &m.detailsViewport
	case tabComments:
		return &m.commentsViewport
	default:
		return nil
	}
}

func (m *model) resetTextViewport(tab detailTab) {
	switch tab {
	case tabDetails:
		m.detailsViewport = textViewport{}
	case tabComments:
		m.commentsViewport = textViewport{}
	}
}

func (m *model) renderTextViewport(viewport *textViewport, height int, content string) string {
	if viewport == nil {
		return content
	}

	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}

	visibleHeight := maxInt(1, height)
	maxY := maxInt(0, len(lines)-visibleHeight)
	viewport.y = clampInt(viewport.y, 0, maxY)

	endY := clampInt(viewport.y+visibleHeight, viewport.y, len(lines))
	visible := lines[viewport.y:endY]

	return strings.Join(visible, "\n")
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

	for token := range strings.FieldsSeq(trimmed) {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			return store.ListFilter{}, fmt.Errorf("invalid filter token %q", token)
		}

		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)

		switch key {
		case "status":
			statuses, err := parseFilterValues(value, issues.IsValidStatus)
			if err != nil {
				return store.ListFilter{}, fmt.Errorf("invalid status %q", err.Error())
			}

			next.Statuses = statuses
		case "type":
			types, err := parseFilterValues(value, issues.IsValidType)
			if err != nil {
				return store.ListFilter{}, fmt.Errorf("invalid type %q", err.Error())
			}

			next.Types = types
		case "label":
			next.Labels = splitFilterValues(value)
		case "assignee":
			next.Assignees = splitFilterValues(value)
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

func parseFilterValues(value string, valid func(string) bool) ([]string, error) {
	values := splitFilterValues(value)
	for _, entry := range values {
		if !valid(entry) {
			return nil, errors.New(entry)
		}
	}

	return values, nil
}

func splitFilterValues(value string) []string {
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		values = append(values, part)
	}

	if len(values) == 0 {
		return nil
	}

	return values
}

func formatFilter(filter store.ListFilter) string {
	parts := []string{}

	if len(filter.Statuses) > 0 {
		parts = append(parts, "status="+strings.Join(filter.Statuses, ","))
	}

	if len(filter.Types) > 0 {
		parts = append(parts, "type="+strings.Join(filter.Types, ","))
	}

	if len(filter.Labels) > 0 {
		parts = append(parts, "label="+strings.Join(filter.Labels, ","))
	}

	if len(filter.Assignees) > 0 {
		parts = append(parts, "assignee="+strings.Join(filter.Assignees, ","))
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

func renderedHeight(parts ...string) int {
	total := 0

	for _, part := range parts {
		if part == "" {
			continue
		}

		total += lipgloss.Height(part)
	}

	return total
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
