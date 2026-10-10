package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Reject ambiguous objects before decoding manifests or component definitions.
// Do not echo field names or values: malformed resources can contain credentials.
func uniqueJSONKeys(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	var value func(int) error
	value = func(depth int) error {
		if depth > 100 {
			return fmt.Errorf("JSON 嵌套过深")
		}
		token, err := d.Token()
		if err != nil {
			return fmt.Errorf("JSON 格式无效")
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				token, err = d.Token()
				if err != nil {
					return fmt.Errorf("JSON 格式无效")
				}
				key, ok := token.(string)
				if !ok || keys[key] {
					return fmt.Errorf("JSON 字段重复或无效")
				}
				keys[key] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("JSON 格式无效")
		}
		_, err = d.Token()
		if err != nil {
			return fmt.Errorf("JSON 格式无效")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("需要一份 JSON")
	}
	return nil
}
