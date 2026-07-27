package settings

import "encoding/json"

func jsonMarshal(v any) (string, error) {
	data, err := json.Marshal(v)
	return string(data), err
}

func jsonUnmarshal(raw string, v any) error {
	return json.Unmarshal([]byte(raw), v)
}
