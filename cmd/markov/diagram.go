package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vokramd/markov/pkg/state"
)

type runNode struct {
	run      *state.Run
	steps    []stepInfo
	children map[string][]*runNode // parentStep -> child runs
}

type stepInfo struct {
	name      string
	status    state.StepStatus
	startedAt *time.Time
}

func buildRunTree(ctx context.Context, store state.Store, runID string) (*runNode, error) {
	run, err := store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}

	dbSteps, err := store.GetSteps(ctx, runID)
	if err != nil {
		return nil, err
	}

	childRuns, err := store.GetChildRuns(ctx, runID)
	if err != nil {
		return nil, err
	}

	node := &runNode{
		run:      run,
		children: make(map[string][]*runNode),
	}

	for _, child := range childRuns {
		childNode, err := buildRunTree(ctx, store, child.RunID)
		if err != nil {
			return nil, err
		}
		node.children[child.ParentStep] = append(node.children[child.ParentStep], childNode)
	}

	node.steps = mergeSteps(dbSteps, node.children)

	return node, nil
}

func mergeSteps(dbSteps []*state.StepResult, children map[string][]*runNode) []stepInfo {
	dbStepNames := make(map[string]bool)
	var all []stepInfo

	for _, s := range dbSteps {
		dbStepNames[s.StepName] = true
		all = append(all, stepInfo{
			name:      s.StepName,
			status:    s.Status,
			startedAt: s.StartedAt,
		})
	}

	for parentStep, childList := range children {
		if dbStepNames[parentStep] {
			continue
		}
		firstChild := childList[0]
		st := &firstChild.run.StartedAt
		status := state.StepCompleted
		for _, child := range childList {
			if child.run.Status == state.RunFailed {
				status = state.StepFailed
				break
			}
		}
		all = append(all, stepInfo{
			name:      parentStep,
			status:    status,
			startedAt: st,
		})
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].startedAt == nil {
			return true
		}
		if all[j].startedAt == nil {
			return false
		}
		return all[i].startedAt.Before(*all[j].startedAt)
	})

	return all
}

func generateMermaid(root *runNode) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")

	idGen := &idAllocator{ids: make(map[string]string)}
	var allNodes []nodeStyle
	renderSubgraph(&b, root, &allNodes, idGen)

	b.WriteString("\n")
	for _, ns := range allNodes {
		switch ns.status {
		case state.StepCompleted:
			fmt.Fprintf(&b, "    class %s completed\n", ns.id)
		case state.StepFailed:
			fmt.Fprintf(&b, "    class %s failed\n", ns.id)
		case state.StepSkipped:
			fmt.Fprintf(&b, "    class %s skipped\n", ns.id)
		}
	}

	b.WriteString("\n")
	b.WriteString("    classDef completed fill:#d4edda,stroke:#28a745\n")
	b.WriteString("    classDef failed fill:#f8d7da,stroke:#dc3545\n")
	b.WriteString("    classDef skipped fill:#e2e3e5,stroke:#6c757d,stroke-dasharray:5 5\n")

	return b.String()
}

type idAllocator struct {
	ids  map[string]string
	next int
}

func (a *idAllocator) runID(runID string) string {
	if id, ok := a.ids[runID]; ok {
		return id
	}
	id := fmt.Sprintf("r%d", a.next)
	a.next++
	a.ids[runID] = id
	return id
}

func (a *idAllocator) stepID(runID, stepName string) string {
	return fmt.Sprintf("%s_%s", a.runID(runID), sanitizeID(stepName))
}

type nodeStyle struct {
	id     string
	status state.StepStatus
}

func renderSubgraph(b *strings.Builder, node *runNode, allNodes *[]nodeStyle, idGen *idAllocator) {
	sgID := idGen.runID(node.run.RunID)
	label := fmt.Sprintf("%s [%s]", node.run.Entrypoint, node.run.RunID)
	fmt.Fprintf(b, "    subgraph %s[\"%s\"]\n", sgID, label)

	for _, step := range node.steps {
		nodeID := idGen.stepID(node.run.RunID, step.name)
		indicator := statusIndicator(step.status)
		fmt.Fprintf(b, "        %s[\"%s %s\"]\n", nodeID, step.name, indicator)
		*allNodes = append(*allNodes, nodeStyle{id: nodeID, status: step.status})
	}

	for i := 0; i < len(node.steps)-1; i++ {
		fromID := idGen.stepID(node.run.RunID, node.steps[i].name)
		toID := idGen.stepID(node.run.RunID, node.steps[i+1].name)
		fmt.Fprintf(b, "        %s --> %s\n", fromID, toID)
	}

	fmt.Fprintf(b, "    end\n")

	for stepName, childList := range node.children {
		parentNodeID := idGen.stepID(node.run.RunID, stepName)
		for _, child := range childList {
			renderSubgraph(b, child, allNodes, idGen)

			if len(child.steps) > 0 {
				childFirstID := idGen.stepID(child.run.RunID, child.steps[0].name)
				fmt.Fprintf(b, "    %s --> %s\n", parentNodeID, childFirstID)
			}
		}
	}
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func statusIndicator(s state.StepStatus) string {
	switch s {
	case state.StepCompleted:
		return "&#10003;"
	case state.StepFailed:
		return "&#10007;"
	case state.StepSkipped:
		return "&#9675;"
	default:
		return "&#9711;"
	}
}
