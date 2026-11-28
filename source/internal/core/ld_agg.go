package core

import (
	"fmt"
	"strings"
)

// parseLdMeasure reads measure=: "" or "count" (messages only), "distinct:F" or "sum:F".
func ParseLdMeasure(s string) (kind, field string, err error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "count" {
		return "", "", nil
	}
	kind, field, ok := strings.Cut(s, ":")
	field = strings.TrimSpace(field)
	if !ok || (kind != "distinct" && kind != "sum") {
		return "", "", fmt.Errorf("measure %q: count, distinct:FIELD or sum:FIELD", s)
	}
	if field == "" || len(field) > 128 || strings.ContainsAny(field, " \t=\"") {
		return "", "", fmt.Errorf("measure %q: name a field (dst_port, bytes …)", s)
	}
	return kind, field, nil
}
