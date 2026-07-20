package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultSnapshotBudgetBytes = 12 * 1024
	defaultSnapshotBudgetNodes = 220
)

type snapshotPresentation struct {
	Text     string
	Mode     string
	Omitted  int
	Artifact string
}

type snapshotTreeNode struct {
	Raw         string
	Normalized  string
	Ref         string
	Parent      int
	Meaningful  bool
	Interactive bool
	Priority    int
}

func storeSessionSnapshot(state *SessionState, statePath string, sequence int, snapshot string, allowDelta, expanded bool, target string, refreshInteractive bool) (snapshotPresentation, error) {
	snapshot = strings.TrimSpace(snapshot)
	var previous string
	if state.LastSnapshot != "" {
		if contents, err := os.ReadFile(state.LastSnapshot); err == nil {
			previous = string(contents)
		}
	}
	path := filepath.Join(state.SessionDir, fmt.Sprintf("action-%04d.snapshot.yml", sequence))
	if err := os.WriteFile(path, []byte(snapshot+"\n"), 0o644); err != nil {
		return snapshotPresentation{}, fmt.Errorf("write session snapshot: %w", err)
	}
	state.LastSnapshot = path
	if err := writeSessionState(statePath, *state); err != nil {
		return snapshotPresentation{}, err
	}

	view := semanticSnapshot(snapshot)
	if expanded {
		view = expandedSemanticSnapshot(snapshot)
	} else if allowDelta && previous != "" {
		view = semanticSnapshotDelta(previous, snapshot, target, refreshInteractive)
	}
	view.Artifact = path
	return view, nil
}

func semanticSnapshot(snapshot string) snapshotPresentation {
	nodes := parseSnapshotTree(snapshot, false)
	if len(nodes) == 0 {
		text := truncateDisplay(strings.TrimSpace(snapshot), defaultSnapshotBudgetBytes)
		if text == "" {
			text = "No semantic content."
		}
		return snapshotPresentation{Text: text, Mode: "full"}
	}
	return semanticSnapshotNodes(nodes, false)
}

func expandedSemanticSnapshot(snapshot string) snapshotPresentation {
	nodes := parseSnapshotTree(snapshot, false)
	if len(nodes) == 0 {
		text := strings.TrimSpace(snapshot)
		if text == "" {
			text = "No semantic content."
		}
		return snapshotPresentation{Text: text, Mode: "full"}
	}
	return semanticSnapshotNodes(nodes, true)
}

func semanticSnapshotNodes(nodes []snapshotTreeNode, expanded bool) snapshotPresentation {
	selected := selectSnapshotNodes(nodes, nil, expanded)
	text, omitted := renderSnapshotSelection(nodes, selected)
	return snapshotPresentation{Text: text, Mode: "full", Omitted: omitted}
}

func semanticSnapshotDelta(previous, current, target string, refreshInteractive bool) snapshotPresentation {
	previousNodes := parseSnapshotTree(previous, true)
	currentNodes := parseSnapshotTree(current, true)
	if len(previousNodes) == 0 || len(currentNodes) == 0 {
		return semanticSnapshotNodes(currentNodes, false)
	}

	previousCounts := make(map[string]int)
	for _, node := range previousNodes {
		if node.Meaningful {
			previousCounts[node.Normalized]++
		}
	}
	changed := make([]bool, len(currentNodes))
	changedCount := 0
	markChanged := func(index int) {
		if !changed[index] {
			changed[index] = true
			changedCount++
		}
	}
	for index, node := range currentNodes {
		if !node.Meaningful {
			continue
		}
		if previousCounts[node.Normalized] > 0 {
			previousCounts[node.Normalized]--
			continue
		}
		markChanged(index)
	}
	actualChanged := changedCount

	if target != "" {
		var targetIdentity string
		targetIndex := -1
		for index, node := range previousNodes {
			if node.Ref == target {
				targetIdentity = node.Normalized
				targetIndex = index
				break
			}
		}
		if targetIdentity != "" {
			targetOccurrence := 0
			for index := 0; index <= targetIndex; index++ {
				if previousNodes[index].Normalized == targetIdentity {
					targetOccurrence++
				}
			}
			occurrence := 0
			for index, node := range currentNodes {
				if node.Normalized == targetIdentity {
					occurrence++
					if occurrence == targetOccurrence {
						markChanged(index)
						break
					}
				}
			}
		}
	}

	var removed []string
	for identity, count := range previousCounts {
		for ; count > 0; count-- {
			removed = append(removed, identity)
		}
	}
	sort.Strings(removed)
	if actualChanged == 0 && len(removed) == 0 {
		markSnapshotStructuralChanges(previousNodes, currentNodes, markChanged)
		actualChanged = changedCount
	}
	meaningful := 0
	for _, node := range currentNodes {
		if node.Meaningful {
			meaningful++
		}
	}
	if actualChanged+len(removed) > meaningful/2 {
		return semanticSnapshot(current)
	}
	if refreshInteractive {
		for index, node := range currentNodes {
			if node.Interactive {
				markChanged(index)
			}
		}
	}
	if changedCount == 0 && len(removed) == 0 {
		return snapshotPresentation{Text: "No semantic changes.", Mode: "delta"}
	}

	selected := selectSnapshotNodes(currentNodes, changed, false)
	text, omitted := renderSnapshotSelection(currentNodes, selected)
	var output strings.Builder
	if text != "" {
		if refreshInteractive {
			output.WriteString("Current after navigation:\n")
		} else {
			output.WriteString("Changed:\n")
		}
		output.WriteString(text)
	}
	if len(removed) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n")
		}
		output.WriteString("Removed:\n")
		limit := len(removed)
		if limit > 20 {
			limit = 20
		}
		for _, identity := range removed[:limit] {
			fmt.Fprintf(&output, "- %s\n", strings.TrimPrefix(identity, "- "))
		}
		if len(removed) > limit {
			fmt.Fprintf(&output, "… %d more removed nodes omitted\n", len(removed)-limit)
			omitted += len(removed) - limit
		}
	}
	result := strings.TrimSpace(output.String())
	full := semanticSnapshotNodes(currentNodes, false)
	if result == "" || len(result) >= len(full.Text) {
		return full
	}
	return snapshotPresentation{Text: result, Mode: "delta", Omitted: omitted}
}

type snapshotStructurePosition struct {
	Index  int
	Parent string
}

func markSnapshotStructuralChanges(previous, current []snapshotTreeNode, mark func(int)) {
	previousCounts := make(map[string]int)
	currentCounts := make(map[string]int)
	for _, node := range previous {
		if node.Meaningful {
			previousCounts[node.Normalized]++
		}
	}
	for _, node := range current {
		if node.Meaningful {
			currentCounts[node.Normalized]++
		}
	}
	positions := make(map[string]snapshotStructurePosition)
	meaningfulIndex := 0
	for index, node := range previous {
		if !node.Meaningful {
			continue
		}
		if previousCounts[node.Normalized] == 1 && currentCounts[node.Normalized] == 1 {
			positions[node.Normalized] = snapshotStructurePosition{Index: meaningfulIndex, Parent: snapshotSemanticParent(previous, index)}
		}
		meaningfulIndex++
	}
	meaningfulIndex = 0
	for index, node := range current {
		if !node.Meaningful {
			continue
		}
		if position, ok := positions[node.Normalized]; ok && (position.Index != meaningfulIndex || position.Parent != snapshotSemanticParent(current, index)) {
			mark(index)
		}
		meaningfulIndex++
	}
}

func snapshotSemanticParent(nodes []snapshotTreeNode, index int) string {
	for parent := nodes[index].Parent; parent >= 0; parent = nodes[parent].Parent {
		if nodes[parent].Meaningful && !strings.HasPrefix(nodes[parent].Normalized, "- generic") {
			return nodes[parent].Normalized
		}
	}
	return ""
}

func parseSnapshotTree(snapshot string, identities bool) []snapshotTreeNode {
	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" {
		return nil
	}
	nodes := make([]snapshotTreeNode, 0, strings.Count(snapshot, "\n")+1)
	stack := make([]int, 0, 16)
	for snapshot != "" {
		raw := snapshot
		if end := strings.IndexByte(snapshot, '\n'); end >= 0 {
			raw = snapshot[:end]
			snapshot = snapshot[end+1:]
		} else {
			snapshot = ""
		}
		if strings.TrimSpace(raw) == "" {
			continue
		}
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		depth := indent / 2
		line := strings.TrimSpace(raw)
		for len(stack) > depth {
			stack = stack[:len(stack)-1]
		}
		parent := -1
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		priority, interactive := snapshotNodePriority(line)
		node := snapshotTreeNode{
			Raw:         line,
			Parent:      parent,
			Meaningful:  priority > 0,
			Interactive: interactive,
			Priority:    priority,
		}
		if identities {
			node.Normalized = normalizeSnapshotLine(line)
			node.Ref = snapshotRef(line)
		}
		index := len(nodes)
		nodes = append(nodes, node)
		stack = append(stack, index)
	}
	return nodes
}

func normalizeSnapshotLine(line string) string {
	var normalized strings.Builder
	normalized.Grow(len(line))
	wrote := false
	space := false
	for index := 0; index < len(line); {
		if snapshotSpace(line[index]) {
			space = wrote
			index++
			continue
		}
		if line[index] == '[' && (strings.HasPrefix(line[index:], "[ref=") || strings.HasPrefix(line[index:], "[box=")) {
			if end := strings.IndexByte(line[index:], ']'); end >= 0 {
				index += end + 1
				continue
			}
		}
		if space {
			normalized.WriteByte(' ')
			space = false
		}
		normalized.WriteByte(line[index])
		wrote = true
		index++
	}
	return normalized.String()
}

func snapshotSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func snapshotRef(line string) string {
	marker := "[ref="
	start := strings.Index(line, marker)
	if start < 0 {
		return ""
	}
	value := line[start+len(marker):]
	end := strings.IndexByte(value, ']')
	if end < 0 {
		return ""
	}
	return value[:end]
}

func snapshotNodePriority(line string) (int, bool) {
	content := strings.TrimPrefix(line, "- ")
	role := content
	if end := strings.IndexAny(role, " [:\""); end >= 0 {
		role = role[:end]
	}
	for _, interactive := range []string{
		"button", "checkbox", "combobox", "link", "menuitem", "option", "radio",
		"searchbox", "slider", "spinbutton", "switch", "tab", "textbox", "treeitem",
	} {
		if role == interactive {
			return 3, true
		}
	}
	if role != "generic" && role != "" {
		return 2, false
	}
	if strings.Contains(content, "[active]") || strings.Contains(content, "[focused]") || strings.Contains(content, "[checked]") {
		return 2, false
	}
	if strings.HasPrefix(strings.TrimPrefix(content, "generic"), " \"") {
		return 1, false
	}
	if colon := strings.Index(content, ":"); colon >= 0 && strings.TrimSpace(content[colon+1:]) != "" {
		return 1, false
	}
	return 0, false
}

func selectSnapshotNodes(nodes []snapshotTreeNode, candidates []bool, expanded bool) []bool {
	selected := make([]bool, len(nodes))
	selectedCount := 0
	selectedBytes := 0
	add := func(index int) bool {
		var chain []int
		for current := index; current >= 0 && !selected[current]; current = nodes[current].Parent {
			chain = append(chain, current)
		}
		cost := 0
		for _, current := range chain {
			cost += len(nodes[current].Raw) + 2
		}
		if !expanded && (selectedCount+len(chain) > defaultSnapshotBudgetNodes || selectedBytes+cost > defaultSnapshotBudgetBytes) {
			return false
		}
		for _, current := range chain {
			selected[current] = true
			selectedCount++
			selectedBytes += len(nodes[current].Raw) + 2
		}
		return true
	}
	for priority := 3; priority >= 1; priority-- {
		for index, node := range nodes {
			if node.Priority != priority || (candidates != nil && !candidates[index]) {
				continue
			}
			add(index)
		}
	}
	return selected
}

func renderSnapshotSelection(nodes []snapshotTreeNode, selected []bool) (string, int) {
	var output strings.Builder
	selectedMeaningful := 0
	meaningful := 0
	selectedChildren := make([]int, len(nodes))
	for index, node := range nodes {
		if selected[index] && node.Parent >= 0 {
			selectedChildren[node.Parent]++
		}
	}
	childDepth := make([]int, len(nodes))
	for index, node := range nodes {
		if node.Meaningful {
			meaningful++
		}
		depth := 0
		if node.Parent >= 0 {
			depth = childDepth[node.Parent]
		}
		if !selected[index] {
			childDepth[index] = depth
			continue
		}
		collapse := !node.Meaningful && selectedChildren[index] == 1
		if !collapse {
			output.WriteString(strings.Repeat("  ", depth))
			output.WriteString(node.Raw)
			output.WriteByte('\n')
			if node.Meaningful {
				selectedMeaningful++
			}
			depth++
		}
		childDepth[index] = depth
	}
	omitted := meaningful - selectedMeaningful
	if omitted > 0 {
		fmt.Fprintf(&output, "… %d semantic nodes omitted; use --full or inspect the snapshot artifact\n", omitted)
	}
	return strings.TrimSpace(output.String()), omitted
}
