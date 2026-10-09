package enginecatalog

// ModelOption describes an engine's advertised model-dependent selector.
// Values are opaque strings. Boolean options use "true" and "false" here;
// their adapter restores the native boolean wire type when executing.
type ModelOption struct {
	ID           string        `json:"id"`
	Label        string        `json:"label"`
	Type         string        `json:"type"`
	DefaultValue string        `json:"default_value"`
	Choices      []ModelChoice `json:"choices"`
}

type ModelChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

func CloneModelOptions(options []ModelOption) []ModelOption {
	if options == nil {
		return nil
	}
	out := append([]ModelOption(nil), options...)
	for i := range out {
		out[i].Choices = append([]ModelChoice(nil), out[i].Choices...)
	}
	return out
}
