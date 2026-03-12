package tui

import (
	"fmt"
	"slices"
	"strings"

	"tack/internal/issues"
)

type graphViewport struct {
	x int
	y int
}

type graphStroke struct {
	horizontal rune
	vertical   rune
}

type graphRect struct {
	x      int
	y      int
	width  int
	height int
}

type graphLayer struct {
	title string
	ids   []string
}

var (
	blockStroke = graphStroke{horizontal: '─', vertical: '│'}
	treeStroke  = graphStroke{horizontal: '┄', vertical: '┆'}
)

func (m *model) handleGraphViewportKey(key string) bool {
	if m.focus != paneDetail {
		return false
	}

	switch m.activeTab {
	case tabFocusedGraph, tabProjectGraph:
	default:
		return false
	}

	viewport := m.activeGraphViewport()
	if viewport == nil {
		return false
	}

	switch key {
	case "h":
		viewport.x = maxInt(0, viewport.x-4)
		return true
	case "l":
		viewport.x += 4
		return true
	case "up", "k":
		viewport.y = maxInt(0, viewport.y-2)
		return true
	case "down", "j":
		viewport.y += 2
		return true
	case "pgup", "ctrl+u":
		viewport.y = maxInt(0, viewport.y-8)
		return true
	case "pgdown", "ctrl+d":
		viewport.y += 8
		return true
	default:
		return false
	}
}

func (m *model) activeGraphViewport() *graphViewport {
	switch m.activeTab {
	case tabFocusedGraph:
		return &m.focusedGraphViewport
	case tabProjectGraph:
		return &m.projectGraphViewport
	default:
		return nil
	}
}

func (m *model) resetGraphViewport(tab detailTab) {
	switch tab {
	case tabFocusedGraph:
		m.focusedGraphViewport = graphViewport{}
	case tabProjectGraph:
		m.projectGraphViewport = graphViewport{}
	}
}

func (m *model) renderGraphViewport(width, height int, viewport *graphViewport, content string) string {
	if viewport == nil {
		return content
	}

	lines := strings.Split(content, "\n")

	maxWidth := 0
	for _, line := range lines {
		maxWidth = maxInt(maxWidth, len([]rune(line)))
	}

	visibleHeight := maxInt(1, height-1)
	maxX := maxInt(0, maxWidth-width)
	maxY := maxInt(0, len(lines)-visibleHeight)

	viewport.x = clampInt(viewport.x, 0, maxX)
	viewport.y = clampInt(viewport.y, 0, maxY)

	visible := clipGraphLines(lines, viewport.x, viewport.y, width, visibleHeight)
	status := fmt.Sprintf("viewport x=%d/%d y=%d/%d", viewport.x, maxX, viewport.y, maxY)

	return strings.Join(append([]string{status}, visible...), "\n")
}

func renderFocusedGraph(view issues.FocusedGraphView) string {
	selected, ok := view.NodeSummaries[view.SelectedID]
	if !ok {
		return "Focused graph unavailable for the selected issue."
	}

	parent, hasParent := view.NodeSummaries[view.ParentID]
	blockers := summariesFromIDs(view.BlockedByIDs, view.NodeSummaries)
	blocked := summariesFromIDs(view.BlocksIDs, view.NodeSummaries)
	children := summariesFromIDs(view.ChildIDs, view.NodeSummaries)

	selectedBox := newGraphBox(selected)
	parentBox := newOptionalGraphBox(parent, hasParent)
	leftBoxes := newGraphBoxes(blockers)
	rightBoxes := newGraphBoxes(blocked)
	childBoxes := newGraphBoxes(children)

	leftWidth := maxBoxWidth(leftBoxes)
	rightWidth := maxBoxWidth(rightBoxes)

	centerWidth := selectedBox.width
	if hasParent {
		centerWidth = maxInt(centerWidth, parentBox.width)
	}

	if len(childBoxes) > 0 {
		centerWidth = maxInt(centerWidth, maxBoxWidth(childBoxes))
	}

	const (
		laneGap     = 8
		stackGap    = 2
		verticalGap = 4
	)

	leftX := 0
	centerX := leftWidth + laneGap
	rightX := centerX + centerWidth + laneGap

	parentY := 2
	selectedY := parentY + optionalBoxHeight(hasParent, parentBox.height) + verticalGap
	childY := selectedY + selectedBox.height + verticalGap

	leftStartY := selectedY
	if len(leftBoxes) > 0 {
		leftStartY = selectedY + (selectedBox.height-stackHeight(leftBoxes, stackGap))/2
	}

	rightStartY := selectedY
	if len(rightBoxes) > 0 {
		rightStartY = selectedY + (selectedBox.height-stackHeight(rightBoxes, stackGap))/2
	}

	childStartY := childY
	totalHeight := maxInt(selectedY+selectedBox.height, childStartY+stackHeight(childBoxes, stackGap))
	totalHeight = maxInt(totalHeight, leftStartY+stackHeight(leftBoxes, stackGap))
	totalHeight = maxInt(totalHeight, rightStartY+stackHeight(rightBoxes, stackGap))
	totalHeight += 2

	totalWidth := maxInt(centerX+centerWidth, rightX+rightWidth)
	totalWidth = maxInt(totalWidth, len([]rune("Legend: ─▶ blocks  ┄▶ parent/child")))
	totalWidth += 2

	canvas := newGraphCanvas(totalWidth, totalHeight)
	canvas.write(0, 0, "Legend: ─▶ blocks  ┄▶ parent/child")
	canvas.write(leftX, maxInt(1, leftStartY-2), "Blocked By")
	canvas.write(centerX, 1, "Parent")
	canvas.write(centerX, maxInt(1, selectedY-2), "Selected")
	canvas.write(rightX, maxInt(1, rightStartY-2), "Blocks")
	canvas.write(centerX, maxInt(selectedY+selectedBox.height+1, childStartY-2), "Children")

	selectedRect := canvas.drawBox(centerX+(centerWidth-selectedBox.width)/2, selectedY, selectedBox)

	var parentRect graphRect
	if hasParent {
		parentRect = canvas.drawBox(centerX+(centerWidth-parentBox.width)/2, parentY, parentBox)
		drawVerticalConnector(canvas, parentRect.bottomCenterX(), parentRect.y+parentRect.height, selectedRect.topCenterX(), selectedRect.y-1, treeStroke, '▼')
	} else {
		canvas.write(centerX, parentY+1, "(none)")
	}

	for i, box := range leftBoxes {
		rect := canvas.drawBox(leftX+(leftWidth-box.width)/2, leftStartY+i*(box.height+stackGap), box)
		drawOrthogonalConnector(canvas, rect.x+rect.width, rect.midY(), selectedRect.x-1, selectedRect.midY(), blockStroke, '▶')
	}

	if len(leftBoxes) == 0 {
		canvas.write(leftX, leftStartY+1, "(none)")
	}

	for i, box := range rightBoxes {
		rect := canvas.drawBox(rightX+(rightWidth-box.width)/2, rightStartY+i*(box.height+stackGap), box)
		drawOrthogonalConnector(canvas, selectedRect.x+selectedRect.width, selectedRect.midY(), rect.x-1, rect.midY(), blockStroke, '▶')
	}

	if len(rightBoxes) == 0 {
		canvas.write(rightX, rightStartY+1, "(none)")
	}

	for i, box := range childBoxes {
		rect := canvas.drawBox(centerX+(centerWidth-box.width)/2, childStartY+i*(box.height+stackGap), box)
		drawVerticalConnector(canvas, selectedRect.bottomCenterX(), selectedRect.y+selectedRect.height, rect.topCenterX(), rect.y-1, treeStroke, '▼')
	}

	if len(childBoxes) == 0 {
		canvas.write(centerX, childStartY+1, "(none)")
	}

	return canvas.render()
}

func renderProjectGraph(view issues.ProjectGraphView, centerID string) string {
	if len(view.Issues) == 0 {
		return "No project graph data."
	}

	summaryByID := map[string]issues.IssueSummary{}
	for _, summary := range view.Issues {
		summaryByID[summary.ID] = summary
	}

	layers := buildProjectLayers(view, centerID)
	columnWidths := make([]int, len(layers))
	positions := map[string]graphRect{}

	const (
		columnGap = 10
		boxGap    = 2
	)

	headerY := 2
	boxTopY := 4

	totalWidth := len([]rune("Legend: ─▶ blocks  ┄▶ parent/child"))
	totalHeight := boxTopY
	currentX := 0

	for i, layer := range layers {
		width := len([]rune(layer.title))
		for _, id := range layer.ids {
			width = maxInt(width, newGraphBox(summaryByID[id]).width)
		}

		columnWidths[i] = width

		currentX += width
		if i < len(layers)-1 {
			currentX += columnGap
		}
	}

	totalWidth = maxInt(totalWidth, currentX)

	currentX = 0

	for i, layer := range layers {
		layerHeight := 0

		for j, id := range layer.ids {
			box := newGraphBox(summaryByID[id])
			x := currentX + (columnWidths[i]-box.width)/2
			y := boxTopY + j*(box.height+boxGap)
			positions[id] = graphRect{x: x, y: y, width: box.width, height: box.height}
			layerHeight = y + box.height
		}

		totalHeight = maxInt(totalHeight, layerHeight)
		currentX += columnWidths[i] + columnGap
	}

	canvas := newGraphCanvas(totalWidth+2, totalHeight+2)
	canvas.write(0, 0, "Legend: ─▶ blocks  ┄▶ parent/child")

	currentX = 0
	for i, layer := range layers {
		canvas.write(currentX, headerY, layer.title)

		for _, id := range layer.ids {
			canvas.drawBox(positions[id].x, positions[id].y, newGraphBox(summaryByID[id]))
		}

		currentX += columnWidths[i] + columnGap
	}

	for _, link := range view.Links {
		sourceRect, sourceOK := positions[link.SourceID]

		targetRect, targetOK := positions[link.TargetID]
		if !sourceOK || !targetOK {
			continue
		}

		stroke := blockStroke
		if link.Kind == "parent_child" {
			stroke = treeStroke
		}

		switch {
		case sourceRect.x < targetRect.x:
			drawOrthogonalConnector(canvas, sourceRect.x+sourceRect.width, sourceRect.midY(), targetRect.x-1, targetRect.midY(), stroke, '▶')
		case sourceRect.x > targetRect.x:
			drawOrthogonalConnector(canvas, sourceRect.x-1, sourceRect.midY(), targetRect.x+targetRect.width, targetRect.midY(), stroke, '◀')
		case sourceRect.y < targetRect.y:
			drawVerticalConnector(canvas, sourceRect.bottomCenterX(), sourceRect.y+sourceRect.height, targetRect.topCenterX(), targetRect.y-1, stroke, '▼')
		default:
			drawVerticalConnector(canvas, sourceRect.topCenterX(), sourceRect.y-1, targetRect.bottomCenterX(), targetRect.y+targetRect.height, stroke, '▲')
		}
	}

	return canvas.render()
}

func buildProjectLayers(view issues.ProjectGraphView, centerID string) []graphLayer {
	if centerID != "" {
		if layers := buildCenteredProjectLayers(view, centerID); len(layers) > 0 {
			return layers
		}
	}

	return buildClusteredProjectLayers(view)
}

func buildCenteredProjectLayers(view issues.ProjectGraphView, centerID string) []graphLayer {
	if centerID == "" {
		return nil
	}

	found := false

	for _, summary := range view.Issues {
		if summary.ID == centerID {
			found = true
			break
		}
	}

	if !found {
		return nil
	}

	forward := map[string][]string{}
	reverse := map[string][]string{}

	for _, link := range view.Links {
		forward[link.SourceID] = append(forward[link.SourceID], link.TargetID)
		reverse[link.TargetID] = append(reverse[link.TargetID], link.SourceID)
	}

	downstream := bfsGraph(centerID, forward)
	upstream := bfsGraph(centerID, reverse)
	grouped := map[int][]string{}
	detached := []string{}

	for _, summary := range view.Issues {
		switch summary.ID {
		case centerID:
			grouped[0] = append(grouped[0], summary.ID)
		default:
			down, downOK := downstream[summary.ID]
			up, upOK := upstream[summary.ID]

			switch {
			case downOK && upOK:
				if up <= down {
					grouped[-up] = append(grouped[-up], summary.ID)
				} else {
					grouped[down] = append(grouped[down], summary.ID)
				}
			case upOK:
				grouped[-up] = append(grouped[-up], summary.ID)
			case downOK:
				grouped[down] = append(grouped[down], summary.ID)
			default:
				detached = append(detached, summary.ID)
			}
		}
	}

	keys := make([]int, 0, len(grouped))
	for depth := range grouped {
		keys = append(keys, depth)
	}

	slices.Sort(keys)

	layers := make([]graphLayer, 0, len(keys)+1)
	for _, depth := range keys {
		title := fmt.Sprintf("Depth %+d", depth)
		if depth == 0 {
			title = "Focus"
		}

		layers = append(layers, graphLayer{title: title, ids: grouped[depth]})
	}

	if len(detached) > 0 {
		layers = append(layers, graphLayer{title: "Detached", ids: detached})
	}

	return layers
}

func buildClusteredProjectLayers(view issues.ProjectGraphView) []graphLayer {
	var (
		roots      []string
		inProgress []string
		ready      []string
		blocked    []string
		closed     []string
		other      []string
	)

	for _, summary := range view.Issues {
		switch {
		case summary.ParentID == "":
			roots = append(roots, summary.ID)
		case summary.Status == issues.StatusInProgress:
			inProgress = append(inProgress, summary.ID)
		case summary.Status == issues.StatusClosed:
			closed = append(closed, summary.ID)
		case summary.Status == issues.StatusBlocked || len(summary.BlockedBy) > 0 || len(summary.OpenChildren) > 0:
			blocked = append(blocked, summary.ID)
		case summary.Status == issues.StatusOpen && summary.Assignee == "":
			ready = append(ready, summary.ID)
		default:
			other = append(other, summary.ID)
		}
	}

	layers := []graphLayer{}
	appendLayer := func(title string, ids []string) {
		if len(ids) == 0 {
			return
		}

		layers = append(layers, graphLayer{title: title, ids: ids})
	}

	appendLayer("Roots", roots)
	appendLayer("In Progress", inProgress)
	appendLayer("Ready", ready)
	appendLayer("Blocked", blocked)
	appendLayer("Closed", closed)
	appendLayer("Other", other)

	return layers
}

func bfsGraph(start string, adjacency map[string][]string) map[string]int {
	distances := map[string]int{}
	queue := []string{start}
	seen := map[string]struct{}{start: {}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, next := range adjacency[current] {
			if _, ok := seen[next]; ok {
				continue
			}

			seen[next] = struct{}{}
			distances[next] = distances[current] + 1
			queue = append(queue, next)
		}
	}

	delete(distances, start)

	return distances
}

func clipGraphLines(lines []string, xOffset, yOffset, width, height int) []string {
	if len(lines) == 0 {
		return []string{""}
	}

	startY := clampInt(yOffset, 0, maxInt(0, len(lines)-1))
	endY := clampInt(startY+height, startY, len(lines))
	out := make([]string, 0, endY-startY)

	for _, line := range lines[startY:endY] {
		runes := []rune(line)
		startX := clampInt(xOffset, 0, len(runes))
		endX := clampInt(startX+width, startX, len(runes))
		visible := string(runes[startX:endX])

		padding := width - len([]rune(visible))
		if padding > 0 {
			visible += strings.Repeat(" ", padding)
		}

		out = append(out, visible)
	}

	for len(out) < height {
		out = append(out, strings.Repeat(" ", width))
	}

	return out
}

type graphBox struct {
	lines  []string
	width  int
	height int
}

func newGraphBoxes(summaries []issues.IssueSummary) []graphBox {
	boxes := make([]graphBox, 0, len(summaries))
	for _, summary := range summaries {
		boxes = append(boxes, newGraphBox(summary))
	}

	return boxes
}

func newOptionalGraphBox(summary issues.IssueSummary, ok bool) graphBox {
	if !ok {
		return graphBox{}
	}

	return newGraphBox(summary)
}

func newGraphBox(summary issues.IssueSummary) graphBox {
	title := trimRunes(summary.Title, 22)
	meta := fmt.Sprintf("[%s] %s", summary.Status, summary.Type)
	idLine := summary.ID

	contentWidth := maxInt(12, len([]rune(idLine)))
	contentWidth = maxInt(contentWidth, len([]rune(title)))
	contentWidth = maxInt(contentWidth, len([]rune(meta)))

	horizontal := '─'
	vertical := '│'

	if summary.Status == issues.StatusClosed {
		horizontal = '┈'
		vertical = '┊'
	}

	lines := []string{
		"┌" + strings.Repeat(string(horizontal), contentWidth+2) + "┐",
		string(vertical) + " " + padRunes(idLine, contentWidth) + " " + string(vertical),
		string(vertical) + " " + padRunes(title, contentWidth) + " " + string(vertical),
		string(vertical) + " " + padRunes(meta, contentWidth) + " " + string(vertical),
		"└" + strings.Repeat(string(horizontal), contentWidth+2) + "┘",
	}

	return graphBox{
		lines:  lines,
		width:  len([]rune(lines[0])),
		height: len(lines),
	}
}

func summariesFromIDs(ids []string, summaries map[string]issues.IssueSummary) []issues.IssueSummary {
	out := make([]issues.IssueSummary, 0, len(ids))
	for _, id := range ids {
		summary, ok := summaries[id]
		if !ok {
			out = append(out, issues.IssueSummary{ID: id, Title: id, Status: "unknown", Type: "task"})
			continue
		}

		out = append(out, summary)
	}

	return out
}

func maxBoxWidth(boxes []graphBox) int {
	width := 0
	for _, box := range boxes {
		width = maxInt(width, box.width)
	}

	return width
}

func stackHeight(boxes []graphBox, gap int) int {
	if len(boxes) == 0 {
		return 0
	}

	height := 0
	for i, box := range boxes {
		height += box.height
		if i < len(boxes)-1 {
			height += gap
		}
	}

	return height
}

func optionalBoxHeight(ok bool, height int) int {
	if !ok {
		return 0
	}

	return height
}

type graphCanvas struct {
	cells [][]rune
}

func newGraphCanvas(width, height int) *graphCanvas {
	cells := make([][]rune, height)
	for y := range cells {
		cells[y] = make([]rune, width)
		for x := range cells[y] {
			cells[y][x] = ' '
		}
	}

	return &graphCanvas{cells: cells}
}

func (c *graphCanvas) render() string {
	lines := make([]string, 0, len(c.cells))
	for _, row := range c.cells {
		lines = append(lines, strings.TrimRight(string(row), " "))
	}

	return strings.Join(lines, "\n")
}

func (c *graphCanvas) write(x, y int, text string) {
	if y < 0 || y >= len(c.cells) {
		return
	}

	for i, r := range []rune(text) {
		c.put(x+i, y, r)
	}
}

func (c *graphCanvas) drawBox(x, y int, box graphBox) graphRect {
	for i, line := range box.lines {
		c.write(x, y+i, line)
	}

	return graphRect{x: x, y: y, width: box.width, height: box.height}
}

func (c *graphCanvas) drawHorizontal(y, x1, x2 int, stroke rune) {
	if y < 0 || y >= len(c.cells) {
		return
	}

	if x1 > x2 {
		x1, x2 = x2, x1
	}

	for x := x1; x <= x2; x++ {
		c.put(x, y, mergeConnectorRune(c.at(x, y), stroke))
	}
}

func (c *graphCanvas) drawVertical(x, y1, y2 int, stroke rune) {
	if x < 0 || len(c.cells) == 0 || x >= len(c.cells[0]) {
		return
	}

	if y1 > y2 {
		y1, y2 = y2, y1
	}

	for y := y1; y <= y2; y++ {
		c.put(x, y, mergeConnectorRune(c.at(x, y), stroke))
	}
}

func (c *graphCanvas) put(x, y int, r rune) {
	if y < 0 || y >= len(c.cells) || x < 0 || x >= len(c.cells[y]) {
		return
	}

	c.cells[y][x] = r
}

func (c *graphCanvas) at(x, y int) rune {
	if y < 0 || y >= len(c.cells) || x < 0 || x >= len(c.cells[y]) {
		return ' '
	}

	return c.cells[y][x]
}

func (r graphRect) midY() int {
	return r.y + r.height/2
}

func (r graphRect) topCenterX() int {
	return r.x + r.width/2
}

func (r graphRect) bottomCenterX() int {
	return r.x + r.width/2
}

func drawOrthogonalConnector(canvas *graphCanvas, startX, startY, endX, endY int, stroke graphStroke, arrow rune) {
	midX := startX + (endX-startX)/2
	canvas.drawHorizontal(startY, startX, midX, stroke.horizontal)
	canvas.drawVertical(midX, startY, endY, stroke.vertical)
	canvas.drawHorizontal(endY, midX, endX, stroke.horizontal)
	canvas.put(endX, endY, arrow)
}

func drawVerticalConnector(canvas *graphCanvas, startX, startY, endX, endY int, stroke graphStroke, arrow rune) {
	if startX != endX {
		drawOrthogonalConnector(canvas, startX, startY, endX, endY, stroke, arrow)
		return
	}

	canvas.drawVertical(startX, startY, endY, stroke.vertical)
	canvas.put(endX, endY, arrow)
}

func mergeConnectorRune(existing, incoming rune) rune {
	switch {
	case existing == ' ':
		return incoming
	case existing == incoming:
		return existing
	case isHorizontal(existing) && isVertical(incoming):
		return '┼'
	case isVertical(existing) && isHorizontal(incoming):
		return '┼'
	case isArrow(existing):
		return existing
	case isArrow(incoming):
		return incoming
	default:
		return incoming
	}
}

func isHorizontal(r rune) bool {
	return strings.ContainsRune("─┄┈┼", r)
}

func isVertical(r rune) bool {
	return strings.ContainsRune("│┆┊┼", r)
}

func isArrow(r rune) bool {
	return strings.ContainsRune("▶◀▼▲", r)
}

func padRunes(value string, width int) string {
	trimmed := trimRunes(value, width)

	padding := width - len([]rune(trimmed))
	if padding <= 0 {
		return trimmed
	}

	return trimmed + strings.Repeat(" ", padding)
}

func trimRunes(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}

	if width <= 1 {
		return string(runes[:width])
	}

	return string(runes[:width-1]) + "…"
}
