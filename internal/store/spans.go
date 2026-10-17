package store

import "encoding/json"

// EncodeSpans stores rune ranges [start, end) as JSON ("" for none).
func EncodeSpans(spans [][2]int) string {
	if len(spans) == 0 {
		return ""
	}
	data, err := json.Marshal(spans)
	if err != nil {
		return ""
	}
	return string(data)
}

// DecodeSpans reads ranges stored by EncodeSpans; malformed or out of
// range ones (for a text of n runes) are dropped.
func DecodeSpans(s string, n int) [][2]int {
	if s == "" {
		return nil
	}
	var spans [][2]int
	if json.Unmarshal([]byte(s), &spans) != nil {
		return nil
	}
	out := spans[:0]
	for _, sp := range spans {
		if sp[0] >= 0 && sp[0] < sp[1] && sp[1] <= n {
			out = append(out, sp)
		}
	}
	return out
}
