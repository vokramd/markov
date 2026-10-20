package engine

import (
	"fmt"
	"log"
	"strings"

	gruleAst "github.com/hyperjumptech/grule-rule-engine/ast"
	gruleBuilder "github.com/hyperjumptech/grule-rule-engine/builder"
	gruleEngine "github.com/hyperjumptech/grule-rule-engine/engine"
	grulePkg "github.com/hyperjumptech/grule-rule-engine/pkg"
	"github.com/jctanner/markov/pkg/parser"
)

type gateResult struct {
	Action     string
	FiredRules []string
	Facts      map[string]any
}

func (e *Engine) evaluateGate(file *parser.WorkflowFile, runID string, ruleNames []string, gateFacts map[string]any, runCtx map[string]any) (*gateResult, error) {
	rules := make([]parser.Rule, 0, len(ruleNames))
	for _, name := range ruleNames {
		r := file.GetRule(name)
		if r == nil {
			return nil, fmt.Errorf("rule %q not found", name)
		}
		rules = append(rules, *r)
	}

	// Build evaluation context: vars + set_fact values + gate facts
	evalCtx := make(map[string]any)
	for k, v := range runCtx {
		evalCtx[k] = v
	}

	for k, v := range gateFacts {
		evalCtx[k] = v
	}

	facts := NewFactStore(evalCtx)

	// Compile YAML rules to GRL
	var grlParts []string
	for _, rule := range rules {
		grl, err := compileRuleToGRL(rule)
		if err != nil {
			return nil, fmt.Errorf("compiling rule %q: %w", rule.Name, err)
		}
		e.verbose("[run:%s]   compiled rule %q to GRL", runID, rule.Name)
		grlParts = append(grlParts, grl)
	}
	grlSource := strings.Join(grlParts, "\n\n")
	e.verbose("[run:%s]   GRL source:\n%s", runID, grlSource)

	// Load into Grule
	knowledgeLibrary := gruleAst.NewKnowledgeLibrary()
	rb := gruleBuilder.NewRuleBuilder(knowledgeLibrary)
	resource := grulePkg.NewBytesResource([]byte(grlSource))

	if err := rb.BuildRuleFromResource("gate", "1.0.0", resource); err != nil {
		return nil, fmt.Errorf("building GRL rules: %w\nGRL source:\n%s", err, grlSource)
	}

	knowledgeBase, err := knowledgeLibrary.NewKnowledgeBaseInstance("gate", "1.0.0")
	if err != nil {
		return nil, fmt.Errorf("creating knowledge base: %w", err)
	}

	dataCtx := gruleAst.NewDataContext()
	if err := dataCtx.Add("Facts", facts); err != nil {
		return nil, fmt.Errorf("adding facts to data context: %w", err)
	}

	ge := gruleEngine.NewGruleEngine()
	ge.MaxCycle = 100
	if err := ge.Execute(dataCtx, knowledgeBase); err != nil {
		return nil, fmt.Errorf("executing rule engine: %w", err)
	}

	// Collect results
	var firedList []string
	gateAction := "continue"
	gateActionSalience := -1
	setFacts := make(map[string]any)

	for _, rule := range rules {
		if !facts.HasFired(rule.Name) {
			continue
		}
		firedList = append(firedList, rule.Name)

		action := rule.Action
		if action == "" {
			action = "continue"
		}

		e.verbose("[run:%s]   rule %q fired (salience=%d, action=%s)", runID, rule.Name, rule.Salience, action)

		if rule.Salience > gateActionSalience {
			gateAction = action
			gateActionSalience = rule.Salience
		}

		for k := range rule.SetFact {
			setFacts[k] = facts.Get(k)
		}
	}

	// Merge set facts back into runCtx
	for k, v := range setFacts {
		runCtx[k] = v
	}

	// Preserve resolved input facts in the receipt for resume. Rule assignments win.
	for k, v := range gateFacts {
		if _, assigned := setFacts[k]; !assigned {
			setFacts[k] = v
			runCtx[k] = v
		}
	}

	if len(firedList) == 0 {
		log.Printf("[run:%s]   no rules matched", runID)
	}

	return &gateResult{
		Action:     gateAction,
		FiredRules: firedList,
		Facts:      setFacts,
	}, nil
}
