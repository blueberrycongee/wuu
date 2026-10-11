package generativeui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"
	"unicode/utf8"
)

const (
	maxProgramNodes       = 256
	maxProgramDepth       = 12
	maxProgramExpressions = 2048
	maxExpressionDepth    = 16
	maxProgramStateBytes  = 16 * 1024
	maxValueDepth         = 8
)

// The program is data, not executable source. The schema closes every syntax
// object; this pass checks references, scope, types, and whole-program budgets.
// A client must validate again before evaluating saved or streamed content.
type programValidator struct {
	states      map[string]any
	ids         map[string]bool
	nodes       int
	expressions int
}

type expressionScope struct {
	item  bool
	event bool
	form  bool
}

func validateProgram(spec map[string]any) error {
	if err := nonblank(spec, "spec", "title", "fallback"); err != nil {
		return err
	}
	state := spec["state"].([]any)
	if programJSONSize(state) > maxProgramStateBytes {
		return fmt.Errorf("spec.state exceeds %d bytes", maxProgramStateBytes)
	}
	v := programValidator{states: make(map[string]any), ids: make(map[string]bool)}
	for index, raw := range state {
		entry := raw.(map[string]any)
		id := entry["id"].(string)
		if _, exists := v.states[id]; exists {
			return fmt.Errorf("spec.state[%d].id %q duplicates another state", index, id)
		}
		if err := validateProgramValue(entry["value"], 1); err != nil {
			return fmt.Errorf("spec.state[%d].value: %w", index, err)
		}
		v.states[id] = entry["value"]
	}
	return v.validateNodes(spec["body"].([]any), "spec.body", 1, expressionScope{})
}

// Match the renderer's UTF-8 JSON.stringify budget. Go's ordinary JSON
// encoder escapes HTML and line separators, making harmless text count larger.
func programJSONSize(value any) int {
	switch typed := value.(type) {
	case string:
		size := 2
		for _, char := range typed {
			switch char {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				size += 2
			default:
				if char < 0x20 {
					size += 6
				} else {
					size += utf8.RuneLen(char)
				}
			}
		}
		return size
	case []any:
		size := 2
		for index, item := range typed {
			if index > 0 {
				size++
			}
			size += programJSONSize(item)
		}
		return size
	case map[string]any:
		size := 2
		for key, item := range typed {
			size += programJSONSize(key) + 1 + programJSONSize(item) + 1
		}
		if len(typed) > 0 {
			size--
		}
		return size
	case float64:
		if typed == 0 {
			return 1
		}
		raw, _ := json.Marshal(value)
		return len(raw)
	default:
		raw, _ := json.Marshal(value)
		return len(raw)
	}
}

func validateProgramValue(value any, depth int) error {
	if depth > maxValueDepth {
		return fmt.Errorf("literal data exceeds %d nesting levels", maxValueDepth)
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if err := validateProgramValue(item, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range typed {
			if !hasText(key) {
				return fmt.Errorf("literal object keys must not be blank")
			}
			if err := validateProgramValue(item, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *programValidator) validateExpression(value any, path string, depth int, scope expressionScope) error {
	v.expressions++
	if v.expressions > maxProgramExpressions || depth > maxExpressionDepth {
		return fmt.Errorf("%s exceeds the expression budget (%d expressions, %d levels)", path, maxProgramExpressions, maxExpressionDepth)
	}
	expression, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	op := expression["op"].(string)
	switch op {
	case "literal":
		if err := validateProgramValue(expression["value"], 1); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case "state":
		key := expression["key"].(string)
		if _, exists := v.states[key]; !exists {
			return fmt.Errorf("%s refers to undeclared state %q", path, key)
		}
	case "item":
		if path, exists := expression["path"]; exists {
			for _, key := range path.([]any) {
				if !hasText(key.(string)) {
					return fmt.Errorf("item path keys must not be blank")
				}
			}
		}
		if !scope.item {
			return fmt.Errorf("%s uses item outside each, map, or filter", path)
		}
	case "event":
		if !scope.event {
			return fmt.Errorf("%s uses event outside an onChange action", path)
		}
	default:
		if op == "object" && len(expression["args"].([]any))%2 != 0 {
			return fmt.Errorf("%s object arguments must be key/value pairs", path)
		}
		for index, arg := range expression["args"].([]any) {
			argScope := scope
			if (op == "map" || op == "filter") && index == 1 {
				argScope.item = true
			}
			if err := v.validateExpression(arg, fmt.Sprintf("%s.args[%d]", path, index), depth+1, argScope); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *programValidator) validateNodes(nodes []any, path string, depth int, scope expressionScope) error {
	if len(nodes) > 0 && depth > maxProgramDepth {
		return fmt.Errorf("%s exceeds %d node nesting levels", path, maxProgramDepth)
	}
	for index, raw := range nodes {
		v.nodes++
		if v.nodes > maxProgramNodes {
			return fmt.Errorf("program exceeds %d nodes", maxProgramNodes)
		}
		node := raw.(map[string]any)
		id := node["id"].(string)
		nodePath := fmt.Sprintf("%s[%d]", path, index)
		if v.ids[id] {
			return fmt.Errorf("%s.id %q duplicates another node", nodePath, id)
		}
		v.ids[id] = true
		kind := node["type"].(string)
		// All listed fields have the same expression syntax. Their resulting
		// value types are checked by the renderer, including after state edits.
		for _, key := range []string{"text", "label", "title", "value", "detail", "disabled", "invalid", "when", "items", "rows", "points"} {
			if value, exists := node[key]; exists {
				if err := v.validateExpression(value, nodePath+"."+key, 1, scope); err != nil {
					return err
				}
			}
		}
		if kind == "progress" {
			if value, exists := node["max"]; exists {
				if err := v.validateExpression(value, nodePath+".max", 1, scope); err != nil {
					return err
				}
			}
		}
		if rawState, bound := node["state"]; bound {
			stateID := rawState.(string)
			value, exists := v.states[stateID]
			if !exists {
				return fmt.Errorf("%s binds undeclared state %q", nodePath, stateID)
			}
			if err := validateBinding(node, value, nodePath); err != nil {
				return err
			}
		}
		for _, event := range []string{"onClick", "onChange", "onSubmit"} {
			if actions, exists := node[event]; exists {
				eventScope := scope
				eventScope.event = event == "onChange"
				if err := v.validateActions(actions.([]any), nodePath+"."+event, eventScope); err != nil {
					return err
				}
			}
		}
		if kind == "table" {
			if err := validateProgramTable(node, nodePath); err != nil {
				return err
			}
		}
		if kind == "chart" {
			if err := v.validateChart(node, nodePath, scope); err != nil {
				return err
			}
		}
		childScope := scope
		if kind == "form" {
			if scope.form {
				return fmt.Errorf("%s cannot nest a form inside another form", nodePath)
			}
			childScope.form = true
		}
		if kind == "each" {
			childScope.item = true
			if key, exists := node["key"]; exists {
				if err := v.validateExpression(key, nodePath+".key", 1, childScope); err != nil {
					return err
				}
			}
		}
		for _, key := range []string{"children", "then", "else"} {
			if children, exists := node[key]; exists {
				if err := v.validateNodes(children.([]any), nodePath+"."+key, depth+1, childScope); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (v *programValidator) validateActions(actions []any, path string, scope expressionScope) error {
	effects := make(map[string]bool)
	for index, raw := range actions {
		action := raw.(map[string]any)
		actionPath := fmt.Sprintf("%s[%d]", path, index)
		kind := action["type"].(string)
		switch kind {
		case "set":
			stateID := action["state"].(string)
			if _, exists := v.states[stateID]; !exists {
				return fmt.Errorf("%s assigns undeclared state %q", actionPath, stateID)
			}
			if err := v.validateExpression(action["value"], actionPath+".value", 1, scope); err != nil {
				return err
			}
		case "copy", "sendMessage":
			if effects[kind] {
				return fmt.Errorf("%s has more than one %s action", path, kind)
			}
			effects[kind] = true
			if kind == "sendMessage" && scope.event {
				return fmt.Errorf("%s can send a message only from an explicit button click or form submission", actionPath)
			}
			if err := v.validateExpression(action["text"], actionPath+".text", 1, scope); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateBinding(node map[string]any, value any, path string) error {
	valid := false
	switch node["type"] {
	case "input":
		if node["inputType"] == "number" {
			_, valid = value.(float64)
		} else {
			text, isString := value.(string)
			valid = isString
			if valid && node["inputType"] == "date" && text != "" {
				date, err := time.Parse("2006-01-02", text)
				if len(text) != 10 || err != nil || date.Year() < 1 || date.Year() > 9999 {
					return fmt.Errorf("%s date state must be empty or a valid YYYY-MM-DD date in years 0001–9999", path)
				}
			}
		}
	case "textarea":
		_, valid = value.(string)
	case "checkbox":
		_, valid = value.(bool)
	case "slider":
		min, max := node["min"].(float64), node["max"].(float64)
		if min >= max {
			return fmt.Errorf("%s.min must be less than max", path)
		}
		number, ok := value.(float64)
		valid = ok && number >= min && number <= max
	case "select", "radio", "segmented":
		options := node["options"].([]any)
		for index, raw := range options {
			option := raw.(map[string]any)
			if err := nonblank(option, fmt.Sprintf("%s.options[%d]", path, index), "label"); err != nil {
				return err
			}
			for _, previous := range options[:index] {
				if reflect.DeepEqual(previous.(map[string]any)["value"], option["value"]) {
					return fmt.Errorf("%s.options[%d].value duplicates another option", path, index)
				}
			}
			valid = valid || reflect.DeepEqual(value, option["value"])
		}
	}
	if !valid {
		return fmt.Errorf("%s initial state must match the control type, options, and range", path)
	}
	return nil
}

func literalValue(value any) (any, bool) {
	if expression, ok := value.(map[string]any); ok {
		return expression["value"], expression["op"] == "literal"
	}
	return value, true
}

func validateProgramTable(node map[string]any, path string) error {
	columns := node["columns"].([]any)
	keys := make(map[string]bool)
	for index, raw := range columns {
		column := raw.(map[string]any)
		key := column["key"].(string)
		if keys[key] {
			return fmt.Errorf("%s.columns[%d].key duplicates another column", path, index)
		}
		keys[key] = true
		if err := nonblank(column, fmt.Sprintf("%s.columns[%d]", path, index), "label"); err != nil {
			return err
		}
	}
	value, literal := literalValue(node["rows"])
	if !literal {
		return nil
	}
	rows, ok := value.([]any)
	if !ok || len(rows) > 200 {
		return fmt.Errorf("%s.rows must evaluate to at most 200 rows", path)
	}
	for index, raw := range rows {
		row, ok := raw.([]any)
		if !ok || len(row) != len(columns) {
			return fmt.Errorf("%s.rows[%d] must contain exactly %d cells", path, index, len(columns))
		}
		for _, cell := range row {
			if !tableScalar(cell) {
				return fmt.Errorf("%s.rows[%d] cells must be scalars, with text at most 500 characters", path, index)
			}
		}
	}
	return nil
}

func tableScalar(value any) bool {
	switch typed := value.(type) {
	case string:
		return len([]rune(typed)) <= 500
	case float64, bool, nil:
		return true
	default:
		return false
	}
}

func (v *programValidator) validateChart(node map[string]any, path string, scope expressionScope) error {
	if err := nonblank(node, path, "xLabel", "yLabel"); err != nil {
		return err
	}
	chartType := node["chartType"].(string)
	stacked := node["stacked"] == true
	if _, present := node["stacked"]; present && chartType != "bar" && chartType != "area" {
		return fmt.Errorf("%s.stacked requires a bar or area chart", path)
	}
	if points, exists := node["points"]; exists {
		if value, literal := literalValue(points); literal {
			return validateProgramPoints(value, path+".points", chartType)
		}
		return nil
	}
	if chartType == "pie" || chartType == "donut" {
		return fmt.Errorf("%s pie and donut charts require points, not series", path)
	}
	seriesList := node["series"].([]any)
	hasScatter := false
	for _, raw := range seriesList {
		kind, _ := raw.(map[string]any)["chartType"].(string)
		if kind == "" {
			kind = chartType
		}
		hasScatter = hasScatter || kind == "scatter"
	}
	ids := make(map[string]bool)
	var categoryLabels []string
	for index, raw := range seriesList {
		series := raw.(map[string]any)
		seriesPath := fmt.Sprintf("%s.series[%d]", path, index)
		id := series["id"].(string)
		if ids[id] {
			return fmt.Errorf("%s.id duplicates another chart series", seriesPath)
		}
		ids[id] = true
		if err := nonblank(series, seriesPath, "label"); err != nil {
			return err
		}
		kind := chartType
		if override, exists := series["chartType"]; exists {
			kind = override.(string)
		}
		if stacked && kind != "bar" && kind != "area" {
			return fmt.Errorf("%s stacked series must be bar or area", seriesPath)
		}
		if err := v.validateExpression(series["points"], seriesPath+".points", 1, scope); err != nil {
			return err
		}
		if points, literal := literalValue(series["points"]); literal {
			if err := validateProgramPoints(points, seriesPath+".points", kind); err != nil {
				return err
			}
			// Category series share one x-axis. Index alignment is only
			// meaningful when each series has the same ordered labels.
			// Scatter mixtures use a numeric x-axis instead.
			if !hasScatter {
				labels := make([]string, len(points.([]any)))
				for index, raw := range points.([]any) {
					labels[index] = raw.(map[string]any)["label"].(string)
				}
				if categoryLabels == nil {
					categoryLabels = labels
				} else if !slices.Equal(categoryLabels, labels) {
					return fmt.Errorf("%s category series must use the same ordered labels", seriesPath)
				}
			}
		}
	}
	return nil
}

func validateProgramPoints(value any, path, chartType string) error {
	points, ok := value.([]any)
	if !ok || len(points) < 1 || len(points) > 100 {
		return fmt.Errorf("%s must evaluate to 1–100 chart points", path)
	}
	for index, raw := range points {
		point, ok := raw.(map[string]any)
		if !ok || len(point) < 2 || len(point) > 3 {
			return fmt.Errorf("%s[%d] must contain label, value, and optional x only", path, index)
		}
		for key := range point {
			if key != "label" && key != "value" && key != "x" {
				return fmt.Errorf("%s[%d] has an unsupported chart point property", path, index)
			}
		}
		x, hasX := point["x"]
		if hasX {
			if _, numeric := x.(float64); !numeric {
				return fmt.Errorf("%s[%d].x must be numeric", path, index)
			}
		} else if chartType == "scatter" {
			return fmt.Errorf("%s[%d] scatter points require numeric x", path, index)
		}
		label, validLabel := point["label"].(string)
		number, validNumber := point["value"].(float64)
		if validNumber && (chartType == "pie" || chartType == "donut") && number < 0 {
			return fmt.Errorf("%s[%d] circular chart values must be nonnegative", path, index)
		}
		if !validLabel || !hasText(label) || len([]rune(label)) > 80 || !validNumber {
			return fmt.Errorf("%s[%d] needs a nonblank label (at most 80 characters) and numeric value", path, index)
		}
	}
	return nil
}

// Bound nesting before recursive JSON schema validation, even for malformed
// programs. Quotes and escapes are tracked so text does not count as syntax.
func boundedJSONNesting(raw []byte, limit int) bool {
	depth := 0
	quoted, escaped := false, false
	for _, char := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				quoted = false
			}
			continue
		}
		switch char {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > limit {
				return false
			}
		case '}', ']':
			depth--
		}
	}
	return true
}
