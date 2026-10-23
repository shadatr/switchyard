package api

import (
	"encoding/json"
	"fmt"
	"maps"
)

// splitObject decodes a JSON object into its raw fields.
func splitObject(b []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	return fields, nil
}

func decodeField(fields map[string]json.RawMessage, name string, dst any) error {
	raw, ok := fields[name]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("field %q: %w", name, err)
	}
	return nil
}

func remaining(fields map[string]json.RawMessage, known []string) map[string]json.RawMessage {
	for _, k := range known {
		delete(fields, k)
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func copyExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(extra)+8)
	maps.Copy(out, extra)
	return out
}

func setField(out map[string]json.RawMessage, name string, v any, omit bool) error {
	if omit {
		delete(out, name)
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("field %q: %w", name, err)
	}
	out[name] = b
	return nil
}
