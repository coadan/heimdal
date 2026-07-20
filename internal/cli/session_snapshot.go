package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultSnapshotBudgetBytes = 12 * 1024
	defaultSnapshotBudgetNodes = 220
)

var (
	snapshotRefPattern = regexp.MustCompile(`\s*\[ref=[^\]]+\]`)
	snapshotBoxPattern = regexp.MustCompile(`\s*\[box=[^\]]+\]`)
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
	Children    []int
	Meaningful  bool
	Interactive bool
	Priority    int
}

func storeSessionSnapshot(state *SessionState, statePath string, sequence int, snapshot string, allowDelta, expanded bool, target string) (snapshotPresentation, error) {
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
		view = semanticSnapshotDelta(previous, snapshot, target)
	}
	view.Artifact = path
	return view, nil
}

func semanticSnapshot(snapshot string) snapshotPresentation {
	nodes := parseSnapshotTree(snapshot)
	if len(nodes) == 0 {
		text := truncateDisplay(strings.TrimSpace(snapshot), defaultSnapshotBudgetBytes)
		if text == "" {
			text = "No semantic content."
		}
		return snapshotPresentation{Text: text, Mode: "full"}
	}
	selected := selectSnapshotNodes(nodes, nil, false)
	text, omitted := renderSnapshotSelection(nodes, selected)
	return snapshotPresentation{Text: text, Mode: "full", Omitted: omitted}
}

func expandedSemanticSnapshot(snapshot string) snapshotPresentation {
	nodes := parseSnapshotTree(snapshot)
	if len(nodes) == 0 {
		text := strings.TrimSpace(snapshot)
		if text == "" {
			text = "No semantic content."
		}
		return snapshotPresentation{Text: text, Mode: "full"}
	}
	selected := selectSnapshotNodes(nodes, nil, true)
	text, omitted := renderSnapshotSelection(nodes, selected)
	return snapshotPresentation{Text: text, Mode: "full", Omitted: omitted}
}

func semanticSnapshotDelta(previous, current, target string) snapshotPresentation {
	previousNodes := parseSnapshotTree(previous)
	currentNodes := parseSnapshotTree(current)
	if len(previousNodes) == 0 || len(currentNodes) == 0 {
		return semanticSnapshot(current)
	}

	previousCounts := make(map[string]int)
	for _, node := range previousNodes {
		if node.Meaningful {
			previousCounts[node.Normalized]++
		}
	}
	changed := make(map[int]bool)
	for index, node := range currentNodes {
		if !node.Meaningful {
			continue
		}
		if previousCounts[node.Normalized] > 0 {
			previousCounts[node.Normalized]--
			continue
		}
		changed[index] = true
	}

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
						changed[index] = true
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
	meaningful := 0
	for _, node := range currentNodes {
		if node.Meaningful {
			meaningful++
		}
	}
	if len(changed)+len(removed) > meaningful/2 {
		return semanticSnapshot(current)
	}
	if len(changed) == 0 && len(removed) == 0 {
		return snapshotPresentation{Text: "No semantic changes.", Mode: "delta"}
	}

	selected := selectSnapshotNodes(currentNodes, changed, false)
	text, omitted := renderSnapshotSelection(currentNodes, selected)
	var output strings.Builder
	if text != "" {
		output.WriteString("Changed:\n")
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
	full := semanticSnapshot(current)
	if result == "" || len(result) >= len(full.Text) {
		return full
	}
	return snapshotPresentation{Text: result, Mode: "delta", Omitted: omitted}
}

func parseSnapshotTree(snapshot string) []snapshotTreeNode {
	var nodes []snapshotTreeNode
	var stack []int
	for _, raw := range strings.Split(strings.TrimSpace(snapshot), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		depth := indent / 2
		line := strings.TrimSpace(raw)
		for len(stack) > depth {
			stack = stack[:len(stack)-1]
		}
		parent := -1
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		normalized := normalizeSnapshotLine(line)
		priority, interactive := snapshotNodePriority(normalized)
		node := snapshotTreeNode{
			Raw:         line,
			Normalized:  normalized,
			Ref:         snapshotRef(line),
			Parent:      parent,
			Meaningful:  priority > 0,
			Interactive: interactive,
			Priority:    priority,
		}
		index := len(nodes)
		nodes = append(nodes, node)
		if parent >= 0 {
			nodes[parent].Children = append(nodes[parent].Children, index)
		}
		stack = append(stack, index)
	}
	return nodes
}

func normalizeSnapshotLine(line string) string {
	line = snapshotRefPattern.ReplaceAllString(line, "")
	line = snapshotBoxPattern.ReplaceAllString(line, "")
	return strings.Join(strings.Fields(line), " ")
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
	lower := strings.ToLower(strings.TrimPrefix(line, "- "))
	role := lower
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
	if strings.Contains(lower, "[active]") || strings.Contains(lower, "[focused]") || strings.Contains(lower, "[checked]") {
		return 2, false
	}
	if strings.HasPrefix(strings.TrimPrefix(lower, "generic"), " \"") {
		return 1, false
	}
	if colon := strings.Index(lower, ":"); colon >= 0 && strings.TrimSpace(lower[colon+1:]) != "" {
		return 1, false
	}
	return 0, false
}

func selectSnapshotNodes(nodes []snapshotTreeNode, candidates map[int]bool, expanded bool) map[int]bool {
	selected := make(map[int]bool)
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
		if !expanded && (len(selected)+len(chain) > defaultSnapshotBudgetNodes || selectedBytes+cost > defaultSnapshotBudgetBytes) {
			return false
		}
		for _, current := range chain {
			selected[current] = true
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

func renderSnapshotSelection(nodes []snapshotTreeNode, selected map[int]bool) (string, int) {
	var output strings.Builder
	selectedMeaningful := 0
	meaningful := 0
	var render func(int, int)
	render = func(index, depth int) {
		node := nodes[index]
		if node.Meaningful {
			meaningful++
		}
		if !selected[index] {
			for _, child := range node.Children {
				render(child, depth)
			}
			return
		}
		selectedChildren := 0
		for _, child := range node.Children {
			if selected[child] {
				selectedChildren++
			}
		}
		collapse := !node.Meaningful && selectedChildren == 1
		if !collapse {
			output.WriteString(strings.Repeat("  ", depth))
			output.WriteString(node.Raw)
			output.WriteByte('\n')
			if node.Meaningful {
				selectedMeaningful++
			}
			depth++
		}
		for _, child := range node.Children {
			render(child, depth)
		}
	}
	for index, node := range nodes {
		if node.Parent < 0 {
			render(index, 0)
		}
	}
	omitted := meaningful - selectedMeaningful
	if omitted > 0 {
		fmt.Fprintf(&output, "… %d semantic nodes omitted; use --full or inspect the snapshot artifact\n", omitted)
	}
	return strings.TrimSpace(output.String()), omitted
}
