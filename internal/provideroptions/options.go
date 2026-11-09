package provideroptions

func Clone(input map[string]any) map[string]any {
	return clone(input, false)
}

func CloneWithoutDisabled(input map[string]any) map[string]any {
	return clone(input, true)
}

func MergeWithoutDisabled(base, override map[string]any) map[string]any {
	out := CloneWithoutDisabled(base)
	if len(override) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]any, len(override))
	}
	MergeIntoWithoutDisabled(out, override)
	return out
}

func MergeIntoWithoutDisabled(dst, src map[string]any) {
	mergeInto(dst, src, true)
}

func MergeValue(dst map[string]any, key string, value any) {
	mergeValue(dst, key, value, false)
}

func clone(input map[string]any, skipDisabled bool) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		if skipDisabled && key == "disabled" {
			continue
		}
		out[key] = cloneValue(value, skipDisabled)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeInto(dst, src map[string]any, skipDisabled bool) {
	for key, value := range src {
		if skipDisabled && key == "disabled" {
			continue
		}
		mergeValue(dst, key, value, skipDisabled)
	}
}

func mergeValue(dst map[string]any, key string, value any, skipDisabled bool) {
	if nested, ok := value.(map[string]any); ok {
		if existing, ok := dst[key].(map[string]any); ok {
			mergeInto(existing, nested, skipDisabled)
			return
		}
	}
	dst[key] = cloneValue(value, skipDisabled)
}

func cloneValue(value any, skipDisabled bool) any {
	switch typed := value.(type) {
	case map[string]any:
		return clone(typed, skipDisabled)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneValue(item, skipDisabled)
		}
		return out
	default:
		return value
	}
}
