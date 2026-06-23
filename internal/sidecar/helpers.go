package sidecar

import "encoding/json"

func decodeRaw(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}
