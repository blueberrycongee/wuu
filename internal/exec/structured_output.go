package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type outputSchemaValidator struct {
	path string
	raw  json.RawMessage
	sch  *jsonschema.Schema
}

func loadOutputSchema(rootDir, inputPath string) (*outputSchemaValidator, error) {
	path := strings.TrimSpace(inputPath)
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootDir, path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve output schema path %q: %w", inputPath, err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read output schema %q: %w", inputPath, err)
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse output schema %q: %w", inputPath, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode output schema %q: %w", inputPath, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(absPath, doc); err != nil {
		return nil, fmt.Errorf("load output schema %q: %w", inputPath, err)
	}
	sch, err := compiler.Compile(absPath)
	if err != nil {
		return nil, fmt.Errorf("compile output schema %q: %w", inputPath, err)
	}
	compact, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("normalize output schema %q: %w", inputPath, err)
	}
	return &outputSchemaValidator{path: absPath, raw: compact, sch: sch}, nil
}

// validate parses the final message for the structured_result payload. The
// schema-driven prompt and any retries happen inside the app-server Run.
func (v *outputSchemaValidator) validate(text string) (any, error) {
	if v == nil {
		return nil, nil
	}
	var value any
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("final answer is not valid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("final answer contains more than one JSON value")
	}
	if err := v.sch.Validate(value); err != nil {
		return nil, fmt.Errorf("final answer does not match output schema: %w", err)
	}
	return value, nil
}
