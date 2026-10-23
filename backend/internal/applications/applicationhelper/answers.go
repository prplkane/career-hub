package applicationhelper

import (
	"bytes"
	"context"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/fixedregex"
	"careerhub.local/backend/internal/isotime"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
	"go.yaml.in/yaml/v4"
)

var answerID = regexp.MustCompile(`^answer_[a-f0-9]{32}$`)
var answerRules = fixedregex.MustCompile(map[string]fixedregex.Definition{
	"required":  {Pattern: `\s*(?:\*|\(required\))\s*$`, IgnoreCase: true},
	"forbidden": {Pattern: `\b(?:password|passcode|one[ -]?time|otp|pin|social security|ssn|tax id|credit card|debit card|card number|cvv|cvc|bank|routing|salary|compensation|date of birth|birth date|dob|age|race|ethnicity|gender|sex|pronoun|disability|veteran|military|marital|religion|politic(?:s|al)?|medical|health|citizen|citizenship|visa|immigration|sponsor|sponsorship|work authori[sz]ation|security clearance|background check|resume|cover letter|upload|attachment|signature|consent|terms|agree|why|describe|explain|tell us|motivation|experience|qualification|skill|availability|start date|first name|last name|full name|preferred name|email|phone|address|street|city|state|province|postal|zip|country|location|linkedin|github|website|portfolio|major|school|university|current status|stem opt|this company|this employer|this organization|current company|position|role|job)\b`, IgnoreCase: true},
})

type answer struct {
	ID          string `json:"id"`
	Question    string `json:"question"`
	QuestionKey string `json:"question_key"`
	ValueKind   string `json:"value_kind"`
	Value       any    `json:"value"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func question(value any) (string, string, *Error) {
	raw, ok := plain(value, maxTextBytes)
	if !ok {
		return "", "", invalid("ApplicationHelperError")
	}
	normalized, err := textcompat.NFKC15(raw)
	if err != nil {
		return "", "", failure("input", "APPLICATION_HELPER_UNICODE_UNSUPPORTED")
	}
	display := strings.Join(strings.FieldsFunc(normalized, textcompat.IsSpace), " ")
	engine, err := answerRules.ForSource(display)
	if err != nil {
		return "", "", failure("input", "APPLICATION_HELPER_UNICODE_UNSUPPORTED")
	}
	text, err := engine.Text(display)
	if err != nil {
		return "", "", failure("input", "APPLICATION_HELPER_UNICODE_UNSUPPORTED")
	}
	if match, ok := text.Match("required", ""); ok {
		display = display[:match.Start()]
	}
	count, alphabetic := utf8.RuneCountInString(display), false
	for _, point := range display {
		alphabetic = alphabetic || textcompat.IsAlpha(point)
	}
	if count < 4 || count > 300 || !alphabetic {
		return "", "", invalid("ApplicationHelperError")
	}
	text, err = engine.Text(display)
	if err != nil || text.Has("forbidden") {
		return "", "", invalid("ApplicationHelperError")
	}
	canonical := strings.TrimRight(textcompat.Fold(display), " ?!:.\t")
	return display, revision([]byte(canonical)), nil
}
func answerValue(kind any, value any) (string, any, *Error) {
	s, ok := kind.(string)
	if !ok {
		return "", nil, invalid("ApplicationHelperError")
	}
	if s == "boolean" {
		v, ok := value.(bool)
		if !ok {
			return "", nil, invalid("ApplicationHelperError")
		}
		return s, v, nil
	}
	if s != "text" && s != "choice" {
		return "", nil, invalid("ApplicationHelperError")
	}
	raw, ok := plain(value, maxTextBytes)
	if !ok {
		return "", nil, invalid("ApplicationHelperError")
	}
	v, err := textcompat.NFKC15(raw)
	if err != nil {
		return "", nil, failure("input", "APPLICATION_HELPER_UNICODE_UNSUPPORTED")
	}
	v = strings.Join(strings.FieldsFunc(v, textcompat.IsSpace), " ")
	if v == "" || utf8.RuneCountInString(v) > 500 {
		return "", nil, invalid("ApplicationHelperError")
	}
	return s, v, nil
}
func validatedAnswer(v cataloginput.Value) (answer, *Error) {
	var result answer
	if !mapping(v, "id", "question", "question_key", "value_kind", "value", "created_at", "updated_at") {
		return result, invalid("ApplicationHelperError")
	}
	for _, key := range []string{"id", "question", "question_key", "value_kind", "created_at", "updated_at"} {
		if lookup(v, key).Kind != "string" {
			return result, invalid("ApplicationHelperError")
		}
	}
	result.ID = lookup(v, "id").Text
	if !answerID.MatchString(result.ID) {
		return result, invalid("ApplicationHelperError")
	}
	var e *Error
	result.Question, result.QuestionKey, e = question(lookup(v, "question").Text)
	if e != nil {
		return answer{}, e
	}
	if result.QuestionKey != lookup(v, "question_key").Text {
		return answer{}, invalid("ApplicationHelperError")
	}
	value := lookup(v, "value")
	var input any
	if value.Kind == "bool" {
		input = value.Boolean
	} else if value.Kind == "string" {
		input = value.Text
	}
	result.ValueKind, result.Value, e = answerValue(lookup(v, "value_kind").Text, input)
	if e != nil {
		return answer{}, e
	}
	result.CreatedAt, result.UpdatedAt = lookup(v, "created_at").Text, lookup(v, "updated_at").Text
	for _, timestamp := range []string{result.CreatedAt, result.UpdatedAt} {
		_, _, err := isotime.Parse(strings.ReplaceAll(timestamp, "Z", "+00:00"), false)
		if err == isotime.ErrRange || err == isotime.ErrLimit {
			return answer{}, failure("validation", "APPLICATION_HELPER_TIMESTAMP_UNSUPPORTED")
		}
		if err != nil {
			return answer{}, invalid("ApplicationHelperError")
		}
	}
	return result, nil
}
func answerBank(raw []byte, present bool) ([]answer, []byte, *Error) {
	if !present {
		return []answer{}, []byte("schema_version: 1\nanswers: []\n"), nil
	}
	v, e := yamlDocument(raw, "ApplicationHelperError")
	if e != nil {
		return nil, nil, e
	}
	if !mapping(v, "schema_version", "answers") || lookup(v, "schema_version").Kind != "int" || lookup(v, "schema_version").Text != "1" {
		return nil, nil, invalid("ApplicationHelperError")
	}
	list := lookup(v, "answers")
	if list.Kind != "sequence" || len(list.Items) > MaxAnswers {
		return nil, nil, invalid("ApplicationHelperError")
	}
	result, ids, keys := []answer{}, map[string]bool{}, map[string]bool{}
	for _, item := range list.Items {
		a, e := validatedAnswer(item)
		if e != nil {
			return nil, nil, e
		}
		if ids[a.ID] || keys[a.QuestionKey] {
			return nil, nil, invalid("ApplicationHelperError")
		}
		ids[a.ID], keys[a.QuestionKey] = true, true
		result = append(result, a)
	}
	return result, raw, nil
}
func publicAnswers(answers []answer) []any {
	result := []any{}
	for _, a := range answers {
		result = append(result, map[string]any{"id": a.ID, "question": a.Question, "value_kind": a.ValueKind, "value": a.Value})
	}
	return result
}
func answerPayload(answers []answer, raw []byte) map[string]any {
	return map[string]any{"contract_version": 2, "revision": revision(raw), "answers": publicAnswers(answers)}
}
func quoted(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
}
func yamlPair(node *yaml.Node, key string, value *yaml.Node) {
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
func answerBytes(ctx context.Context, answers []answer) ([]byte, *Error) {
	if len(answers) > MaxAnswers {
		return nil, failure("encode", "APPLICATION_HELPER_OUTPUT_LIMIT")
	}
	doc, list := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	yamlPair(doc, "schema_version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	yamlPair(doc, "answers", list)
	for _, a := range answers {
		if ctx.Err() != nil {
			return nil, canceled()
		}
		node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, pair := range [][2]string{{"id", a.ID}, {"question", a.Question}, {"question_key", a.QuestionKey}, {"value_kind", a.ValueKind}} {
			yamlPair(node, pair[0], quoted(pair[1]))
		}
		var value *yaml.Node
		if b, ok := a.Value.(bool); ok {
			text := "false"
			if b {
				text = "true"
			}
			value = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: text}
		} else if s, ok := a.Value.(string); ok {
			value = quoted(s)
		} else {
			return nil, failure("encode", "APPLICATION_HELPER_OUTPUT_INVALID")
		}
		yamlPair(node, "value", value)
		yamlPair(node, "created_at", quoted(a.CreatedAt))
		yamlPair(node, "updated_at", quoted(a.UpdatedAt))
		list.Content = append(list.Content, node)
	}
	var output bytes.Buffer
	writer := yaml.NewEncoder(&output)
	writer.SetIndent(2)
	if err := writer.Encode(doc); err != nil {
		_ = writer.Close()
		return nil, failure("encode", "APPLICATION_HELPER_OUTPUT_INVALID")
	}
	if err := writer.Close(); err != nil {
		return nil, failure("encode", "APPLICATION_HELPER_OUTPUT_INVALID")
	}
	raw := output.Bytes()
	if len(raw) > MaxInputBytes {
		return nil, failure("encode", "APPLICATION_HELPER_OUTPUT_LIMIT")
	}
	restored, _, e := answerBank(raw, true)
	if e != nil || !reflect.DeepEqual(answers, restored) {
		return nil, failure("encode", "APPLICATION_HELPER_OUTPUT_CHANGED_VALUE")
	}
	return raw, nil
}
func saveAnswer(answers []answer, payload map[string]any, now string, newID func() (string, error)) ([]answer, *Error) {
	display, key, e := question(payload["question"])
	if e != nil {
		return nil, e
	}
	// The prototype removes one trailing required marker, writes, then removes
	// another during readback and discovers a different question key. Refuse
	// non-idempotent display normalization before creating a corrupt bank.
	readback, readbackKey, e := question(display)
	if e != nil || readback != display || readbackKey != key {
		return nil, invalid("ApplicationHelperError")
	}
	kind, value, e := answerValue(payload["value_kind"], payload["value"])
	if e != nil {
		return nil, e
	}
	index := slices.IndexFunc(answers, func(a answer) bool { return a.QuestionKey == key })
	if index < 0 {
		if len(answers) >= MaxAnswers {
			return nil, invalid("ApplicationHelperError")
		}
		id, err := newID()
		if err != nil || !answerID.MatchString(id) || slices.ContainsFunc(answers, func(a answer) bool { return a.ID == id }) {
			return nil, failure("identity", "APPLICATION_HELPER_ID_UNAVAILABLE")
		}
		answers = append(answers, answer{ID: id, QuestionKey: key, CreatedAt: now})
		index = len(answers) - 1
	}
	answers[index].Question, answers[index].ValueKind, answers[index].Value, answers[index].UpdatedAt = display, kind, value, now
	slices.SortStableFunc(answers, func(a, b answer) int {
		return strings.Compare(textcompat.Fold(a.Question), textcompat.Fold(b.Question))
	})
	return answers, nil
}
