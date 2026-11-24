package applicationhelper

import (
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func TestValidateAnswerFileMatchesReader(t *testing.T) {
	if e := ValidateAnswerFile([]byte("schema_version: 1\nanswers: []\n")); e != nil {
		t.Fatal("empty saved-answer file refused", e)
	}
	for _, raw := range []string{
		"",
		"[]\n",
		"schema_version: 2\nanswers: []\n",
		"schema_version: 1\nanswers: {}\n",
		"schema_version: 1\nanswers: []\nextra: true\n",
		"schema_version: 1\nanswers: [" + strings.Repeat("null,", 201) + "]",
		"schema_version: 1\nanswers:\n- {id: answer_0123456789abcdef0123456789abcdef, question: What is your password?, question_key: x, value_kind: text, value: x, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z'}\n",
		strings.Repeat("#", cataloginput.YAMLMaxInputBytes+1),
	} {
		if ValidateAnswerFile([]byte(raw)) == nil {
			t.Fatalf("invalid saved-answer file accepted: %.60q", raw)
		}
	}
}
