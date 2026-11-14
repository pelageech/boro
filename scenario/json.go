package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func ReadJSON[T any](reader io.Reader) (T, error) {
	var value T
	data, err := io.ReadAll(reader)
	if err != nil {
		return value, fmt.Errorf("read JSON: %w", err)
	}
	tokens := json.NewDecoder(bytes.NewReader(data))
	if err := inspectJSON(tokens); err != nil {
		return value, fmt.Errorf("invalid JSON document: %w", err)
	}
	if _, err := tokens.Token(); err != io.EOF {
		return value, invalid("json", "exactly one document is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode JSON: %w", err)
	}
	return value, nil
}

func inspectJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON token: %w", err)
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	keys := make(map[string]bool)
	for decoder.More() {
		if delim == '{' {
			token, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("read JSON key: %w", err)
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return invalid("json", "duplicate or invalid object key")
			}
			keys[key] = true
		}
		if err := inspectJSON(decoder); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("close JSON object or array: %w", err)
	}
	return nil
}
