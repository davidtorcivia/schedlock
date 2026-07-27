package web

import (
	"bytes"
	"encoding/json"
)

// prettyJSON renders raw JSON for display, indented and with HTML escaping
// left on so that a payload string can never break out of the surrounding
// element.
func prettyJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}

	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return string(raw)
	}
	return indented.String()
}
