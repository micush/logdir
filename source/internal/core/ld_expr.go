package core

import (
	"strings"
)

// A search compiled for the log store: groups separated by "or", each a list of terms that must all hold.  A term is text to find
// (a phrase, * wildcards) or a field term (ld_fields.go): field=value, field>n …  A word that looks like name=value is a field term
// of its own even inside a phrase, so "failed password user=root" is the phrase "failed password" and the field user=root.

type ldTerm struct {
	neg   bool
	text  string       // lower case, when field is nil
	Field *ldFieldTerm `json:"-"` // a field term
}

type LdGroup []ldTerm

func ldCompile(expr matchExpr) []LdGroup {
	out := make([]LdGroup, 0, len(expr))
	for _, group := range expr {
		var g LdGroup
		for _, term := range group {
			neg := strings.HasPrefix(term, "!")
			body := strings.TrimPrefix(term, "!")
			var run []string
			flush := func() {
				if len(run) > 0 {
					g = append(g, ldTerm{neg: neg, text: strings.Join(run, " ")})
					run = nil
				}
			}
			for _, w := range strings.Split(body, " ") {
				if ft, ok := ldParseFieldTerm(w); ok {
					flush()
					g = append(g, ldTerm{neg: neg, Field: &ft})
					continue
				}
				run = append(run, w)
			}
			flush()
		}
		out = append(out, g)
	}
	return out
}
