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
	ListFilterValues(context.Context, store.FilterValueSource, store.FilterValueKey, store.ListFilter) ([]string, error)
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

type filterPickerMode string

const (
	filterPickerHidden filterPickerMode = ""
	filterPickerKeys   filterPickerMode = "keys"
	filterPickerValues filterPickerMode = "values"
	filterPickerLimit  filterPickerMode = "limit"
)

type filterPickerKey string

const (
	filterPickerKeyStatus   filterPickerKey = "status"
	filterPickerKeyType     filterPickerKey = "type"
	filterPickerKeyLabel    filterPickerKey = "label"
	filterPickerKeyAssignee filterPickerKey = "assignee"
	filterPickerKeyLimit    filterPickerKey = "limit"
	filterPickerKeyReset    filterPickerKey = "reset"
)

type filterPickerOption struct {
	key   filterPickerKey
	label string
}

type filterPickerState struct {
	mode       filterPickerMode
	keyIndex   int
	valueIndex int
	key        filterPickerKey
	values     []string
	selected   map[string]struct{}
	limitInput string
}

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

	filterPicker filterPickerState

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
		filter:    sanitizeFilterForSource(options.DataSource(), options.Filter),
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
	if m.filterPicker.mode != filterPickerHidden {
		return m.handleFilterPickerKey(key)
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
		m.openFilterPicker()
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

	if m.filterPicker.mode != filterPickerHidden {
		filterMaxHeight := m.maxFilterPickerHeight(header, footer, help)
		parts = append(parts, filterStyle.Render(m.renderFilterPicker(filterMaxHeight)))
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

func (m *model) maxFilterPickerHeight(header, footer, help string) int {
	baseHeight := renderedHeight(header, footer, help)

	available := m.height - baseHeight - 1
	if available < 3 {
		return 3
	}

	return available
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
	return "q quit  tab/left/right switch  j/k move or scroll  enter pin/select  / filters  g/G graph  ? more"
}

func (m *model) renderExpandedHelp() string {
	return strings.Join([]string{
		"Controls",
		"q quit, ? toggle help, tab/shift+tab and left/right switch panes or tabs",
		"j/k and up/down move the issue selection",
		"/ opens guided filters; choose status, type, label, assignee, limit, or reset",
		"In value pickers, space toggles the highlighted value and enter applies the selection",
		"In the limit prompt, type digits and press enter; an empty value clears the limit",
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

	m.filter = sanitizeFilterForSource(m.source, m.filter)

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

func (m *model) filterOptions(key store.FilterValueKey) ([]string, error) {
	source := store.FilterValueSourceAll
	if m.source == DataSourceReady {
		source = store.FilterValueSourceReady
	}

	return m.reader.ListFilterValues(m.ctx, source, key, m.filter)
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

func (m *model) openFilterPicker() {
	m.filterPicker = filterPickerState{mode: filterPickerKeys}
}

func (m *model) closeFilterPicker() {
	m.filterPicker = filterPickerState{}
}

func (m *model) handleFilterPickerKey(key string) tea.Cmd {
	switch m.filterPicker.mode {
	case filterPickerKeys:
		return m.handleFilterPickerKeyList(key)
	case filterPickerValues:
		return m.handleFilterPickerValues(key)
	case filterPickerLimit:
		return m.handleFilterPickerLimit(key)
	default:
		return nil
	}
}

func (m *model) handleFilterPickerKeyList(key string) tea.Cmd {
	options := m.filterPickerOptions()
	if len(options) == 0 {
		m.closeFilterPicker()
		return nil
	}

	switch key {
	case "esc":
		m.closeFilterPicker()
	case "up", "k":
		m.filterPicker.keyIndex = clampInt(m.filterPicker.keyIndex-1, 0, len(options)-1)
	case "down", "j":
		m.filterPicker.keyIndex = clampInt(m.filterPicker.keyIndex+1, 0, len(options)-1)
	case "enter":
		selected := options[clampInt(m.filterPicker.keyIndex, 0, len(options)-1)]

		switch selected.key {
		case filterPickerKeyLimit:
			m.filterPicker.mode = filterPickerLimit
			if m.filter.Limit > 0 {
				m.filterPicker.limitInput = strconv.Itoa(m.filter.Limit)
			} else {
				m.filterPicker.limitInput = ""
			}
		case filterPickerKeyReset:
			m.filter = sanitizeFilterForSource(m.source, store.ListFilter{})
			m.closeFilterPicker()
			m.reloadFilterState()
		default:
			err := m.openFilterValuePicker(selected.key)
			if err != nil {
				m.lastError = err
			}
		}
	}

	return nil
}

func (m *model) openFilterValuePicker(key filterPickerKey) error {
	values, err := m.filterOptions(key.storeKey())
	if err != nil {
		return err
	}

	selectedValues := filterValuesForKey(m.filter, key)
	values = mergeFilterValues(values, selectedValues)

	selected := make(map[string]struct{}, len(selectedValues))
	for _, value := range selectedValues {
		selected[value] = struct{}{}
	}

	m.filterPicker.mode = filterPickerValues
	m.filterPicker.key = key
	m.filterPicker.values = values
	m.filterPicker.selected = selected
	m.filterPicker.valueIndex = 0

	if len(values) > 0 {
		for i, value := range values {
			if _, ok := selected[value]; ok {
				m.filterPicker.valueIndex = i
				break
			}
		}
	}

	return nil
}

func (m *model) handleFilterPickerValues(key string) tea.Cmd {
	values := m.filterPicker.values

	switch key {
	case "esc":
		m.filterPicker.mode = filterPickerKeys
		m.filterPicker.key = ""
		m.filterPicker.values = nil
		m.filterPicker.selected = nil
		m.filterPicker.valueIndex = 0
	case "up", "k":
		if len(values) > 0 {
			m.filterPicker.valueIndex = clampInt(m.filterPicker.valueIndex-1, 0, len(values)-1)
		}
	case "down", "j":
		if len(values) > 0 {
			m.filterPicker.valueIndex = clampInt(m.filterPicker.valueIndex+1, 0, len(values)-1)
		}
	case "space":
		if len(values) == 0 {
			return nil
		}

		value := values[clampInt(m.filterPicker.valueIndex, 0, len(values)-1)]
		if _, ok := m.filterPicker.selected[value]; ok {
			delete(m.filterPicker.selected, value)
		} else {
			m.filterPicker.selected[value] = struct{}{}
		}
	case "enter":
		next := applyFilterValuesForKey(m.filter, m.filterPicker.key, orderedSelectedValues(values, m.filterPicker.selected))
		m.filter = sanitizeFilterForSource(m.source, next)
		m.closeFilterPicker()
		m.reloadFilterState()
	}

	return nil
}

func (m *model) handleFilterPickerLimit(key string) tea.Cmd {
	switch key {
	case "esc":
		m.filterPicker.mode = filterPickerKeys
		m.filterPicker.limitInput = ""
	case "backspace", "ctrl+h":
		if len(m.filterPicker.limitInput) > 0 {
			m.filterPicker.limitInput = m.filterPicker.limitInput[:len(m.filterPicker.limitInput)-1]
		}
	case "enter":
		next := m.filter

		if strings.TrimSpace(m.filterPicker.limitInput) == "" {
			next.Limit = 0
		} else {
			limit, err := strconv.Atoi(m.filterPicker.limitInput)
			if err != nil || limit < 0 {
				m.lastError = fmt.Errorf("invalid limit %q", m.filterPicker.limitInput)
				return nil
			}

			next.Limit = limit
		}

		m.filter = sanitizeFilterForSource(m.source, next)
		m.closeFilterPicker()
		m.reloadFilterState()
	default:
		if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
			m.filterPicker.limitInput += key
		}
	}

	return nil
}

func (m *model) reloadFilterState() {
	err := m.reload()
	if err != nil {
		m.lastError = err
	}
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

func (m *model) renderFilterPicker(maxHeight int) string {
	lines := []string{
		sectionTitleStyle.Render("Guided Filters"),
		"Current: " + formatFilter(m.filter),
	}
	contentHeight := maxInt(1, maxHeight-filterStyle.GetVerticalFrameSize())

	switch m.filterPicker.mode {
	case filterPickerValues:
		lines = append(lines,
			"Editing: "+string(m.filterPicker.key),
			"space toggles  enter applies  esc back",
		)

		if len(m.filterPicker.values) == 0 {
			lines = append(lines, mutedTextStyle.Render("No values available for the current filters. Press enter to clear this filter or esc to go back."))
			return strings.Join(lines, "\n")
		}

		start, end := listViewportWindow(len(m.filterPicker.values), m.filterPicker.valueIndex, maxInt(1, contentHeight-len(lines)))
		for i := start; i < end; i++ {
			value := m.filterPicker.values[i]

			marker := "  "
			if i == clampInt(m.filterPicker.valueIndex, 0, len(m.filterPicker.values)-1) {
				marker = "> "
			}

			check := "[ ]"
			if _, ok := m.filterPicker.selected[value]; ok {
				check = "[x]"
			}

			lines = append(lines, fmt.Sprintf("%s%s %s", marker, check, value))
		}
	case filterPickerLimit:
		lines = append(lines,
			"Editing: limit",
			"Type digits and press enter. Leave it empty to clear the limit.",
			"> "+m.filterPicker.limitInput,
		)
	default:
		lines = append(lines, "Choose a filter key to edit.")

		options := m.filterPickerOptions()

		start, end := listViewportWindow(len(options), m.filterPicker.keyIndex, maxInt(1, contentHeight-len(lines)))
		for i := start; i < end; i++ {
			option := options[i]

			marker := "  "
			if i == clampInt(m.filterPicker.keyIndex, 0, len(options)-1) {
				marker = "> "
			}

			lines = append(lines, fmt.Sprintf("%s%-8s %s", marker, option.label, m.renderFilterPickerKeySummary(option.key)))
		}
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

	header := tableHeaderStyle.Render(fmt.Sprintf("%-*s %-*s %-*s %-*s", indicatorWidth, "", idWidth, "ID", statusWidth, "STATUS", typeWidth, "TYPE"))
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

		line := fmt.Sprintf("%-*s %-*s %-*s %-*s", indicatorWidth, marker, idWidth, summary.ID, statusWidth, summary.Status, typeWidth, summary.Type)
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

func sanitizeFilterForSource(source DataSource, filter store.ListFilter) store.ListFilter {
	filter.Statuses = append([]string(nil), filter.Statuses...)
	filter.Types = append([]string(nil), filter.Types...)
	filter.Labels = append([]string(nil), filter.Labels...)
	filter.Assignees = append([]string(nil), filter.Assignees...)

	if source == DataSourceReady {
		filter.Assignees = nil
	}

	return filter
}

func (m *model) filterPickerOptions() []filterPickerOption {
	options := []filterPickerOption{
		{key: filterPickerKeyStatus, label: "status"},
		{key: filterPickerKeyType, label: "type"},
		{key: filterPickerKeyLabel, label: "label"},
	}

	if m.source != DataSourceReady {
		options = append(options, filterPickerOption{key: filterPickerKeyAssignee, label: "assignee"})
	}

	options = append(options,
		filterPickerOption{key: filterPickerKeyLimit, label: "limit"},
		filterPickerOption{key: filterPickerKeyReset, label: "reset"},
	)

	return options
}

func (m *model) renderFilterPickerKeySummary(key filterPickerKey) string {
	switch key {
	case filterPickerKeyStatus:
		return renderFilterValuesSummary(m.filter.Statuses)
	case filterPickerKeyType:
		return renderFilterValuesSummary(m.filter.Types)
	case filterPickerKeyLabel:
		return renderFilterValuesSummary(m.filter.Labels)
	case filterPickerKeyAssignee:
		return renderFilterValuesSummary(m.filter.Assignees)
	case filterPickerKeyLimit:
		if m.filter.Limit <= 0 {
			return "(none)"
		}

		return strconv.Itoa(m.filter.Limit)
	case filterPickerKeyReset:
		if formatFilter(m.filter) == "(none)" {
			return "clear all filters"
		}

		return "clear " + formatFilter(m.filter)
	default:
		return "(none)"
	}
}

func renderFilterValuesSummary(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}

	return strings.Join(values, ", ")
}

func (k filterPickerKey) storeKey() store.FilterValueKey {
	switch k {
	case filterPickerKeyStatus:
		return store.FilterValueKeyStatus
	case filterPickerKeyType:
		return store.FilterValueKeyType
	case filterPickerKeyLabel:
		return store.FilterValueKeyLabel
	case filterPickerKeyAssignee:
		return store.FilterValueKeyAssignee
	default:
		return ""
	}
}

func filterValuesForKey(filter store.ListFilter, key filterPickerKey) []string {
	switch key {
	case filterPickerKeyStatus:
		return append([]string(nil), filter.Statuses...)
	case filterPickerKeyType:
		return append([]string(nil), filter.Types...)
	case filterPickerKeyLabel:
		return append([]string(nil), filter.Labels...)
	case filterPickerKeyAssignee:
		return append([]string(nil), filter.Assignees...)
	default:
		return nil
	}
}

func applyFilterValuesForKey(filter store.ListFilter, key filterPickerKey, values []string) store.ListFilter {
	next := sanitizeFilterForSource(DataSourceAll, filter)

	switch key {
	case filterPickerKeyStatus:
		next.Statuses = values
	case filterPickerKeyType:
		next.Types = values
	case filterPickerKeyLabel:
		next.Labels = values
	case filterPickerKeyAssignee:
		next.Assignees = values
	}

	return next
}

func mergeFilterValues(discovered, selected []string) []string {
	merged := make([]string, 0, len(discovered)+len(selected))
	seen := make(map[string]struct{}, len(discovered)+len(selected))

	for _, value := range discovered {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		merged = append(merged, value)
	}

	for _, value := range selected {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		merged = append(merged, value)
	}

	return merged
}

func orderedSelectedValues(options []string, selected map[string]struct{}) []string {
	if len(selected) == 0 {
		return nil
	}

	values := make([]string, 0, len(selected))
	for _, option := range options {
		if _, ok := selected[option]; ok {
			values = append(values, option)
		}
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

func listViewportWindow(total, selected, visible int) (int, int) {
	if total <= 0 {
		return 0, 0
	}

	visible = maxInt(1, visible)
	selected = clampInt(selected, 0, total-1)

	if total <= visible {
		return 0, total
	}

	start := 0
	if selected >= visible {
		start = selected - visible + 1
	}

	start = clampInt(start, 0, total-visible)

	return start, start + visible
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
