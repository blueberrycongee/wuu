package composition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// UnmarshalJSON rejects misspelled fields and ambiguous selections before they
// can accidentally turn an explicit product into discovery-based defaults.
func (c *Config) UnmarshalJSON(data []byte) error {
	if err := checkJSON(json.NewDecoder(bytes.NewReader(data)), "composition"); err != nil {
		return err
	}
	type plain Config
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("composition: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("composition: unexpected trailing JSON")
	}
	*c = Config(value)
	return nil
}

// A null module selection would otherwise decode as omitted (loading defaults),
// or false (excluding a patch entry). Duplicate keys overwrite earlier choices.
func checkJSON(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if token == nil && (path == "composition" || strings.HasSuffix(path, ".modules") || strings.Contains(path, ".modules.")) {
		return fmt.Errorf("%s: null is not a composition selection; omit the field or use an empty array/object", path)
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key := keyToken.(string)
			if seen[key] {
				return fmt.Errorf("%s: duplicate field %q", path, key)
			}
			seen[key] = true
			if err := checkJSON(decoder, path+"."+key); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
	case json.Delim('['):
		for index := 0; decoder.More(); index++ {
			if err := checkJSON(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
	}
	return err
}
