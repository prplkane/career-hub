// Package eligibilityfacts interprets explicit profile labels, not legal
// eligibility. Missing, negated or historical prose does not establish a fact.
package eligibilityfacts

import (
	"strings"

	"careerhub.local/backend/internal/textcompat"
)

// OPTStatus preserves the qualified evaluator-v5 status contract. The second
// result identifies unconfirmed prose mentioning OPT; it is not a hard blocker.
func OPTStatus(status string) (confirmed, ambiguous bool) {
	text := textcompat.Fold(textcompat.Strip(status))
	text = strings.Join(strings.FieldsFunc(strings.NewReplacer("-", " ", "(", " ", ")", " ").Replace(text), textcompat.IsSpace), " ")
	switch text {
	case "opt", "stem opt", "f1 opt", "f1 stem opt", "f 1 opt", "f 1 stem opt":
		return true, false
	}
	for _, token := range strings.FieldsFunc(text, func(r rune) bool { return !textcompat.IsWord(r) }) {
		if token == "opt" {
			return false, true
		}
	}
	return false, false
}
