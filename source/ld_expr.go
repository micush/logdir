package main

import "strings"

// A search compiled for the log store: groups separated by "or", each a list of terms that must all hold.  A term is text to find
// (a phrase, * wildcards) or a field term (ld_fields.go): field=value, field>n …  A word that looks like name=value is a field term
// of its own even inside a phrase, so "failed password user=root" is the phrase "failed password" and the field user=root.

type ldTerm struct {
	neg   bool
	text  string       // lower case, when field is nil
	field *ldFieldTerm // a field term
}

type ldGroup []ldTerm

func ldCompile(expr matchExpr) []ldGroup {
	out := make([]ldGroup, 0, len(expr))
	for _, group := range expr {
		var g ldGroup
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
					g = append(g, ldTerm{neg: neg, field: &ft})
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

// hasFieldTerms: does the search name a field?
func ldHasFieldTerms(gs []ldGroup) bool {
	for _, g := range gs {
		for _, t := range g {
			if t.field != nil {
				return true
			}
		}
	}
	return false
}
