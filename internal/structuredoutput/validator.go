package structuredoutput

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const MaxRetries = 2

type Validator struct {
	raw json.RawMessage
	sch *jsonschema.Schema
}

func New(raw json.RawMessage) (*Validator, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("parse output schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode output schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource("schema.json", doc); err != nil {
		return nil, fmt.Errorf("load output schema: %w", err)
	}
	sch, err := compiler.Compile("schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile output schema: %w", err)
	}
	compact, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("normalize output schema: %w", err)
	}
	return &Validator{raw: compact, sch: sch}, nil
}

func (v *Validator) InitialPrompt(prompt string) string {
	if v == nil {
		return prompt
	}
	return v.prompt("You must complete the task and make your final answer a single JSON value that validates against this JSON Schema.", prompt)
}

func (v *Validator) RetryPrompt(previous string, validationErr error) string {
	if v == nil {
		return ""
	}
	message := "Your previous final answer did not validate against the required JSON Schema."
	if validationErr != nil {
		message += "\nValidation error: " + validationErr.Error()
	}
	return v.prompt(message, "Previous final answer:\n"+previous)
}

func (v *Validator) Validate(text string) error {
	if v == nil {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
	var value any
	if err := dec.Decode(&value); err != nil {
		return fmt.Errorf("final answer is not valid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("final answer contains more than one JSON value")
	}
	if err := v.sch.Validate(value); err != nil {
		return fmt.Errorf("final answer does not match output schema: %w", err)
	}
	return nil
}

func (v *Validator) prompt(instruction, content string) string {
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\nReturn only JSON. Do not wrap it in Markdown. Do not include explanatory text outside the JSON value.\n\nJSON Schema:\n")
	b.Write(v.raw)
	if strings.TrimSpace(content) != "" {
		b.WriteString("\n\nTask:\n")
		b.WriteString(content)
	}
	return b.String()
}
