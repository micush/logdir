package logs

import (
	"logdir/internal/core"
)

// hasFieldTerms: does the search name a field?
func ldHasFieldTerms(gs []core.LdGroup) bool {
	for _, g := range gs {
		for _, t := range g {
			if t.Field != nil {
				return true
			}
		}
	}
	return false
}
