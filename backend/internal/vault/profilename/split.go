// Package profilename reads the given and family parts that application forms
// ask for out of the single display name a resume review finds.
package profilename

import "strings"

// Split returns the given and family name for a two-word display name such as
// "Ada Lovelace". It never guesses: one word, or three or more ("Mary Ann
// Smith", "Juan de la Cruz"), returns ok false and callers leave both parts
// for the user to fill once. A wrong split on a form is worse than a blank.
//
// Resume extraction only offers names of two to four letter-only words with
// credentials already stripped (intakereview.profileNameCandidate); Vault
// setup offers whatever the user typed.
func Split(name string) (given, family string, ok bool) {
	words := strings.Fields(name)
	if len(words) != 2 {
		return "", "", false
	}
	return words[0], words[1], true
}
