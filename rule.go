// Copyright 2021 Juan Pablo Tosso
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http:// www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package coraza

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jptosso/coraza-waf/v2/types"
	"github.com/jptosso/coraza-waf/v2/types/variables"
	"go.uber.org/zap"
)

type RuleTransformationTools struct {
	Logger *zap.Logger
}

type RuleTransformation = func(input string, tools RuleTransformationTools) string

// This interface is used by this rule's actions
type RuleAction interface {
	// Initializes an action, will be done during compilation
	Init(*Rule, string) error
	// Evaluate will be done during rule evaluation
	Evaluate(*Rule, *Transaction)
	// Type will return the rule type, it's used by Evaluate
	// to choose when to evaluate each action
	Type() types.RuleActionType
}

type ruleActionParams struct {
	// The name of the action, used for logging
	Name string

	// The action to be executed
	Function RuleAction
}

// Operator interface is used to define rule @operators
type RuleOperator interface {
	// Init is used during compilation to setup and cache
	// the operator
	Init(string) error
	// Evaluate is used during the rule evaluation,
	// it returns true if the operator succeeded against
	// the input data for the transaction
	Evaluate(*Transaction, string) bool
}

// RuleOperator is a container for an operator,
type ruleOperatorParams struct {
	// Operator to be used
	Operator RuleOperator

	// Function name (ex @rx)
	Function string
	// Data to initialize the operator
	Data string
	// If true, rule will match if op.Evaluate returns false
	Negation bool
}

type ruleVariableException struct {
	// The string key for the variable that is going to be requested
	// If KeyRx is not nil, KeyStr is ignored
	KeyStr string

	// The key for the variable that is going to be requested
	// If nil, KeyStr is going to be used
	KeyRx *regexp.Regexp
}

// RuleVariable is compiled during runtime by transactions
// to get values from the transaction's variables
// It supports xml, regex, exceptions and many more features
type ruleVariableParams struct {
	// We store the name for performance
	Name string

	// If true, the count of results will be returned
	Count bool

	// The VARIABLE that will be requested
	Variable variables.RuleVariable

	// The key for the variable that is going to be requested
	// If nil, KeyStr is going to be used
	KeyRx *regexp.Regexp

	// The string key for the variable that is going to be requested
	// If KeyRx is not nil, KeyStr is ignored
	KeyStr string

	// A slice of key exceptions
	Exceptions []ruleVariableException
}

type ruleTransformationParams struct {
	// The transformation to be used, used for logging
	Name string

	// The transformation function to be used
	Function RuleTransformation
}

// Rule is used to test a Transaction against certain operators
// and execute actions
type Rule struct {
	// Contains a list of variables that will be compiled
	// by a transaction
	variables []ruleVariableParams

	// Contains a pointer to the operator struct used
	// SecActions and SecMark can have nil Operators
	operator *ruleOperatorParams

	// List of transformations to be evaluated
	// In the future, transformations might be run by the
	// action itself, not sure yet
	transformations []ruleTransformationParams

	// Slice of initialized actions to be evaluated during
	// the rule evaluation process
	actions []ruleActionParams

	// Contains the Id of the parent rule if you are inside
	// a chain. Otherwise it will be 0
	ParentId int

	// Used to mark a rule as a secmarker and alter flows
	SecMark string

	// Contains the raw rule code
	Raw string

	// Contains the child rule to chain, nil if there are no chains
	Chain *Rule

	// Where is this rule stored
	File string

	// Line of the file where this rule was found
	Line int

	// METADATA
	// Rule unique identifier, can be a an int
	Id int

	// Rule tag list
	Tags []string

	// Rule execution phase 1-5
	Phase types.RulePhase

	// Message text to be macro expanded and logged
	// In future versions we might use a special type of string that
	// supports cached macro expansions. For performance
	Msg string

	// Rule revision value
	Rev string

	// Rule maturity index
	Maturity int

	// RuleSet Version
	Version string

	// Rule accuracy
	Accuracy int

	// Rule severity
	Severity types.RuleSeverity

	// Rule logdata
	LogData string

	// If true and this rule is matched, this rule will be
	// written to the audit log
	// If no auditlog, this rule won't be logged
	Log bool

	// If true, the transformations will be multimatched
	MultiMatch bool

	// Used for error logging
	Disruptive bool
}

// Evaluate will evaluate the current rule for the indicated transaction
// If the operator matches, actions will be evaluated and it will return
// the matched variables, keys and values (MatchData)
func (r *Rule) Evaluate(tx *Transaction) []MatchData {
	rid := r.Id
	if rid == 0 {
		rid = r.ParentId
	}
	tx.Waf.Logger.Debug("Evaluating rule",
		zap.Int("rule", rid),
		zap.String("raw", r.Raw),
		zap.String("tx", tx.Id),
		zap.String("event", "EVALUATE_RULE"),
	)
	matchedValues := []MatchData{}
	tx.GetCollection(variables.Rule).SetData(map[string][]string{
		"id":       {strconv.Itoa(rid)},
		"msg":      {r.Msg},
		"rev":      {r.Rev},
		"logdata":  {tx.MacroExpansion(r.LogData)},
		"severity": {r.Severity.String()},
	})
	// secmarkers and secactions will always match
	tools := RuleTransformationTools{
		Logger: tx.Waf.Logger,
	}

	// SecMark and SecAction uses nil operator
	if r.operator == nil {
		tx.Waf.Logger.Debug("Forcing rule match", zap.String("txid", tx.Id),
			zap.Int("rule", r.Id),
			zap.String("event", "RULE_FORCE_MATCH"),
		)
		matchedValues = []MatchData{
			{
				Key:   "",
				Value: "",
			},
		}
	} else {
		ecol := tx.ruleRemoveTargetById[r.Id]
		for _, v := range r.variables {
			var values []MatchData
			for _, c := range ecol {
				if c.Variable == v.Variable {
					// TODO shall we check the pointer?
					v.Exceptions = append(v.Exceptions, ruleVariableException{c.KeyStr, nil})
				}
			}

			values = tx.GetField(v)
			if len(values) == 0 {
				// TODO should we run the operators here?
				continue
			}
			tx.Waf.Logger.Debug("Expanding arguments",
				zap.Int("rule", rid),
				zap.String("tx", tx.Id),
				zap.Int("count", len(values)),
			)
			for _, arg := range values {
				var args []string
				tx.Waf.Logger.Debug("Transforming argument",
					zap.Int("rule", rid),
					zap.String("tx", tx.Id),
					zap.String("argument", arg.Value),
				)
				if r.MultiMatch {
					// TODO in the future, we don't need to run every transformation
					// We could try for each until found
					args = r.executeTransformationsMultimatch(arg.Value, tools)
				} else {
					args = []string{r.executeTransformations(arg.Value, tools)}
				}
				tx.Waf.Logger.Debug("Arguments transformed",
					zap.Int("rule", rid),
					zap.String("tx", tx.Id),
					zap.Strings("arguments", args),
				)

				// args represents the transformed variables
				for _, carg := range args {
					match := r.executeOperator(carg, tx)
					if match {
						matchedValues = append(matchedValues, MatchData{
							VariableName: v.Variable.Name(),
							Variable:     arg.Variable,
							Key:          arg.Key,
							Value:        carg,
						})
					}
					tx.Waf.Logger.Debug("Evaluate rule operator", zap.String("txid", tx.Id),
						zap.Int("rule", rid),
						zap.String("event", "EVALUATE_RULE_OPERATOR"),
						zap.String("operator", r.operator.Function), // TODO fix
						zap.String("data", carg),
						zap.String("variable", arg.Variable.Name()),
						zap.String("key", arg.Key),
						zap.String("value", carg),
						zap.Bool("result", match),
					)
				}
			}
		}
	}

	if len(matchedValues) == 0 {
		// No match for variables
		return matchedValues
	}

	tx.Waf.Logger.Debug("Attempting to match values", zap.String("txid", tx.Id),
		zap.Int("rule", rid),
		zap.String("event", "EVALUATE_RULE_OPERATOR"),
		zap.Any("values", matchedValues))
	// we must match the vars before running the chains
	tx.MatchVariable(matchedValues[0])

	// We run non disruptive actions even if there is no chain match
	for _, a := range r.actions {
		if a.Function.Type() == types.ActionTypeNondisruptive {
			a.Function.Evaluate(r, tx)
		}
	}

	if r.Chain != nil {
		nr := r.Chain
		tx.Waf.Logger.Debug("Evaluating rule chain", zap.Int("rule", rid), zap.String("raw", nr.Raw))
		for nr != nil {
			m := nr.Evaluate(tx)
			if len(m) == 0 {
				// we fail the chain
				return []MatchData{}
			}

			matchedValues = append(matchedValues, m...)
			nr = nr.Chain
		}
	}
	if r.ParentId == 0 {
		// action log is required to add the rule to matched rules
		if r.Log {
			tx.MatchRule(MatchedRule{
				Rule:            *r,
				MatchedData:     matchedValues[0],
				Message:         tx.MacroExpansion(r.Msg),
				Data:            tx.MacroExpansion(r.LogData),
				Uri:             tx.GetCollection(variables.RequestURI).GetFirstString(""),
				Id:              tx.Id,
				Disruptive:      r.Disruptive,
				ServerIpAddress: tx.GetCollection(variables.ServerAddr).GetFirstString(""),
				ClientIpAddress: tx.GetCollection(variables.RemoteAddr).GetFirstString(""),
			})
		}
		// we need to add disruptive actions in the end, otherwise they would be triggered without their chains.
		tx.Waf.Logger.Debug("detecting rule disruptive action", zap.String("txid", tx.Id), zap.Int("rule", r.Id))
		for _, a := range r.actions {
			if a.Function.Type() == types.ActionTypeDisruptive || a.Function.Type() == types.ActionTypeFlow {
				tx.Waf.Logger.Debug("evaluating rule disruptive action", zap.String("txid", tx.Id), zap.Int("rule", rid))
				a.Function.Evaluate(r, tx)
			}
		}
	}
	tx.Waf.Logger.Debug("finished evaluating rule", zap.String("txid", tx.Id),
		zap.Int("rule", rid),
		zap.Int("matched_values", len(matchedValues)),
		zap.String("event", "FINISH_RULE"),
	)
	return matchedValues
}

// AddAction adds an action to the rule
func (r *Rule) AddAction(name string, action RuleAction) error {
	// TODO add more logic, like one persistent action per rule etc
	r.actions = append(r.actions, ruleActionParams{name, action})
	return nil
}

// AddVariable adds a variable to the rule
// The key can be a regexp.Regexp, a string or nil, in case of regexp
// it will be used to match the variable, in case of string it will
// be a fixed match, in case of nil it will match everything
func (r *Rule) AddVariable(v variables.RuleVariable, key interface{}, iscount bool) error {
	var re *regexp.Regexp
	str := ""
	switch v := key.(type) {
	case *regexp.Regexp:
		re = v
		str = re.String()
	case string:
		str = strings.ToLower(v)
	case nil:
		// we allow this
	default:
		return fmt.Errorf("invalid key type %T", v)
	}
	r.variables = append(r.variables, ruleVariableParams{
		Count:      iscount,
		Variable:   v,
		KeyStr:     str,
		KeyRx:      re,
		Exceptions: []ruleVariableException{},
	})
	return nil
}

func (r *Rule) AddVariableNegation(v variables.RuleVariable, key interface{}) error {
	counter := 0
	var re *regexp.Regexp
	str := ""
	switch v := key.(type) {
	case string:
		st := v
		if st == "" {
			return fmt.Errorf("invalid variable negation key, it cannot be empty")
		}
		str = st
	case *regexp.Regexp:
		if v.String() == "" {
			return fmt.Errorf("invalid variable negation key, it cannot be an empty regex")
		}
		re = v
		str = re.String()
	default:
		return fmt.Errorf("invalid negation input %s, %T", v, v)
	}
	for i, rv := range r.variables {
		if rv.Variable == v {
			rv.Exceptions = append(rv.Exceptions, ruleVariableException{str, re})
			r.variables[i] = rv
			counter++
		}
	}
	if counter == 0 {
		return fmt.Errorf("cannot create a variable exception is the variable %q is not used", v.Name())
	}
	return nil
}

// AddTransformation adds a transformation to the rule
// it fails if the transformation cannot be found
func (r *Rule) AddTransformation(name string, t RuleTransformation) error {
	if t == nil || name == "" {
		return fmt.Errorf("invalid transformation %q not found", name)
	}
	r.transformations = append(r.transformations, ruleTransformationParams{name, t})
	return nil
}

// ClearTransformations clears all the transformations
// it is mostly used by the "none" transformation
func (r *Rule) ClearTransformations() {
	r.transformations = []ruleTransformationParams{}
}

func (r *Rule) SetOperator(operator RuleOperator, function string, params string) {
	r.operator = &ruleOperatorParams{
		Operator: operator,
		Function: function,
		Data:     params,
		Negation: (len(function) > 0 && function[0] == '!'),
	}
}

func (r *Rule) executeOperator(data string, tx *Transaction) bool {
	result := r.operator.Operator.Evaluate(tx, data)
	if r.operator.Negation && result {
		return false
	}
	if r.operator.Negation && !result {
		return true
	}
	return result
}

func (r *Rule) executeTransformationsMultimatch(value string, tools RuleTransformationTools) []string {
	res := []string{value}
	for _, t := range r.transformations {
		value = t.Function(value, tools)
		res = append(res, value)
	}
	return res
}

func (r *Rule) executeTransformations(value string, tools RuleTransformationTools) string {
	for _, t := range r.transformations {
		value = t.Function(value, tools)
	}
	return value
}

// NewRule returns a new initialized rule
func NewRule() *Rule {
	return &Rule{
		Phase: 2,
		Tags:  []string{},
	}
}
