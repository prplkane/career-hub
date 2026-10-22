package applicationchecklist

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/applicationreview"
	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/textcompat"
)

type engine struct {
	ctx           context.Context
	tx            *sql.Tx
	clock         string
	prepared      *Prepared
	changed       bool
	lineCache     map[bool]evidenceLines
	evidenceBytes int
}

type evidenceLines struct {
	lines []string
	sizes []int
}

// ApplyInTransaction borrows the existing owner's transaction. Even the final
// post-write projection is composed inside it: no success or failed projection
// can escape after an independently committed write.
func (p *Prepared) ApplyInTransaction(ctx context.Context, tx *sql.Tx, clock string) (*applications.MutationResult, *Error) {
	if err := active(ctx); err != nil {
		return nil, err
	}
	if p == nil || tx == nil {
		return nil, unsupported("CONFIGURATION_INVALID")
	}
	e := engine{ctx: ctx, tx: tx, clock: clock, prepared: p}
	var result map[string]any
	var err *Error
	switch p.request.Operation {
	case "load":
		result, err = e.compose()
	case "add_manual":
		result, err = e.add()
	case "set_state":
		result, err = e.set()
	default:
		return nil, unsupported("REQUEST_UNSUPPORTED")
	}
	if err != nil {
		return nil, err
	}
	if err := active(ctx); err != nil {
		return nil, err
	}
	raw, je := json.Marshal(result)
	if je != nil || len(raw) > applications.MaxResultBytes {
		return nil, unsupported("RESPONSE_LIMIT")
	}
	return &applications.MutationResult{Changed: e.changed, Result: raw}, nil
}

func (e *engine) review(op string, fields map[string]any) (json.RawMessage, *Error) {
	fields["job_id"] = e.prepared.id
	result, p := applicationreview.ApplyInTransaction(e.ctx, e.tx, applicationreview.Request{Operation: op, Fields: fields}, e.clock)
	if p != nil {
		return nil, &Error{p.Stage, p.Code, p.SourceClass, p.SourceMessage}
	}
	if result == nil {
		return nil, unsupported("LEDGER_RESPONSE_UNSUPPORTED")
	}
	e.changed = e.changed || result.Changed
	return result.Result, nil
}

func (e *engine) history(op string) ([]map[string]any, int, *Error) {
	raw, p := e.review(op, map[string]any{})
	if p != nil {
		return nil, 0, p
	}
	var rows []map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&rows) != nil || rows == nil {
		return nil, 0, unsupported("LEDGER_RESPONSE_UNSUPPORTED")
	}
	return rows, len(raw), nil
}

func integer(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case json.Number:
		n, e := v.Int64()
		return n, e == nil
	}
	return 0, false
}

func publicItem(raw map[string]any, origin string) (map[string]any, *Error) {
	item := map[string]any{"origin": origin}
	for _, key := range []string{"fingerprint", "category", "confidence", "source_text"} {
		x, ok := raw[key].(string)
		if !ok || !utf8.ValidString(x) {
			return nil, unsupported("ITEM_UNSUPPORTED")
		}
		item[key] = x
	}
	if !sha(item["fingerprint"].(string)) || !category(item["category"]) || !slices.Contains([]string{"explicit", "review"}, item["confidence"].(string)) {
		return nil, unsupported("ITEM_UNSUPPORTED")
	}
	text := item["source_text"].(string)
	if utf8.RuneCountInString(text) > 1200 || textcompat.Strip(text) == "" {
		return nil, unsupported("ITEM_UNSUPPORTED")
	}
	for _, key := range []string{"line_start", "line_end", "extractor_version"} {
		n, ok := integer(raw[key])
		if !ok || n < 0 {
			return nil, unsupported("ITEM_UNSUPPORTED")
		}
		item[key] = n
	}
	if item["line_start"].(int64) < 1 || item["line_end"].(int64) < item["line_start"].(int64) {
		return nil, unsupported("ITEM_UNSUPPORTED")
	}
	return item, nil
}

func collapsed(text string) string {
	return strings.Join(strings.FieldsFunc(text, textcompat.IsSpace), " ")
}

// Detected evidence uses Python splitlines and the extractor's collapsed line
// wording. Manual evidence uses exact text and newline-only line numbering.
// Neither an old action nor an equal normalized JD hash proves exact evidence.
func (e *engine) evidence(item map[string]any, detected bool) *Error {
	if p := active(e.ctx); p != nil {
		return p
	}
	text := item["source_text"].(string)
	fp, err := applicationanalysis.InstructionFingerprint(item["category"].(string), text)
	if err != nil || fp != item["fingerprint"] {
		return unsupported("EVIDENCE_MISMATCH")
	}
	start, end := item["line_start"].(int64), item["line_end"].(int64)
	// Bound line splitting before allocating it. Valid detected instructions
	// are within the extractor's 10,000-line scan; manual excerpts may be later.
	if e.lineCache == nil {
		e.lineCache = map[bool]evidenceLines{}
	}
	cached, exists := e.lineCache[detected]
	if !exists {
		breaks := 0
		for _, c := range e.prepared.text {
			if c == '\n' || detected && (c == '\v' || c == '\f' || c >= 0x1c && c <= 0x1e || c == 0x85 || c == 0x2028 || c == 0x2029) {
				breaks++
			}
			if breaks > 100000 {
				return unsupported("SOURCE_LINE_LIMIT")
			}
		}
		if detected {
			cached.lines = textcompat.SplitLines(e.prepared.text)
		} else {
			cached.lines = strings.Split(e.prepared.text, "\n")
		}
		cached.sizes = make([]int, len(cached.lines)+1)
		for i, line := range cached.lines {
			cached.sizes[i+1] = cached.sizes[i] + len(line)
		}
		e.lineCache[detected] = cached
	}
	if end > int64(len(cached.lines)) {
		return unsupported("EVIDENCE_MISMATCH")
	}
	cost := cached.sizes[end] - cached.sizes[start-1] + int(end-start)
	if cost > maxHistoryBytes-e.evidenceBytes {
		return unsupported("EVIDENCE_WORK_LIMIT")
	}
	e.evidenceBytes += cost
	span := strings.Join(cached.lines[start-1:end], "\n")
	if detected {
		span, text = collapsed(span), collapsed(text)
	}
	if !strings.Contains(span, text) {
		return unsupported("EVIDENCE_MISMATCH")
	}
	return nil
}

func (e *engine) compose() (map[string]any, *Error) {
	p := e.prepared
	detected := field(p.analysis, "items")
	if detected.Kind != "sequence" {
		return nil, invalid("Saved instruction analysis is invalid")
	}
	if len(detected.Items) > maxItems {
		return nil, unsupported("ITEM_LIMIT")
	}
	manual, mb, failure := e.history("list_manual")
	if failure != nil {
		return nil, failure
	}
	actions, ab, failure := e.history("list_actions")
	if failure != nil {
		return nil, failure
	}
	if len(manual)+len(actions) > maxItems || mb+ab > maxHistoryBytes {
		return nil, unsupported("HISTORY_LIMIT")
	}
	latest, history := map[string]map[string]any{}, map[string]int{}
	for _, action := range actions {
		fp, ok := action["fingerprint"].(string)
		source, sok := action["source_fingerprint"].(string)
		state, aok := action["action"].(string)
		origin, ook := action["origin"].(string)
		_, tok := action["recorded_at"].(string)
		if !ok || !sha(fp) || !sok || !sha(source) || !tok || !aok || !slices.Contains([]string{"pending", "done", "not_applicable"}, state) || !ook || !slices.Contains([]string{"detected", "manual"}, origin) {
			return nil, unsupported("ACTION_UNSUPPORTED")
		}
		history[fp]++
		if latest[fp] == nil {
			latest[fp] = action
		}
	}
	current := []map[string]any{}
	byFingerprint := map[string]bool{}
	for _, raw := range detected.Items {
		if failure := active(e.ctx); failure != nil {
			return nil, failure
		}
		if raw.Kind != "mapping" {
			continue
		}
		value, failure := yamlItem(raw)
		if failure != nil {
			return nil, failure
		}
		item, failure := publicItem(value, "detected")
		if failure != nil {
			return nil, failure
		}
		if failure := e.evidence(item, true); failure != nil {
			return nil, failure
		}
		byFingerprint[item["fingerprint"].(string)] = true
		current = append(current, item)
	}
	for _, raw := range manual {
		fp, ok := raw["fingerprint"].(string)
		source, sok := raw["source_fingerprint"].(string)
		if !ok || !sha(fp) || !sok || !sha(source) {
			return nil, unsupported("ITEM_UNSUPPORTED")
		}
		if source != p.fingerprint || byFingerprint[fp] {
			continue
		}
		item, failure := publicItem(raw, "manual")
		if failure != nil {
			return nil, failure
		}
		if failure := e.evidence(item, false); failure != nil {
			return nil, failure
		}
		byFingerprint[fp] = true
		current = append(current, item)
	}
	if len(current) > maxItems {
		return nil, unsupported("ITEM_LIMIT")
	}
	for _, item := range current {
		fp := item["fingerprint"].(string)
		last := latest[fp]
		stale := last != nil && last["source_fingerprint"] != p.fingerprint
		item["state"], item["stale"], item["previous_state"], item["recorded_at"], item["history_count"] = "pending", stale, "", "", history[fp]
		if last != nil {
			item["recorded_at"] = last["recorded_at"]
			if stale {
				item["previous_state"] = last["action"]
			} else {
				item["state"] = last["action"]
			}
		}
	}
	staleItems := []map[string]any{}
	staleIndex := map[[2]string]int{}
	for _, rows := range [][]map[string]any{manual, actions} {
		for _, raw := range rows {
			if failure := active(e.ctx); failure != nil {
				return nil, failure
			}
			fp := raw["fingerprint"].(string)
			if byFingerprint[fp] {
				continue
			}
			key := [2]string{fp, raw["source_fingerprint"].(string)}
			manualItem := raw["origin"] == "manual"
			_, isAction := raw["action"]
			if _, exists := staleIndex[key]; exists && isAction {
				continue
			}
			origin := "manual"
			if isAction && !manualItem {
				origin = "detected"
			}
			item, failure := publicItem(raw, origin)
			if failure != nil {
				return nil, failure
			}
			item["source_fingerprint"], item["state"], item["stale"], item["previous_state"], item["recorded_at"], item["history_count"] = key[1], "pending", true, "", "", history[fp]
			if isAction {
				item["state"], item["previous_state"], item["recorded_at"] = raw["action"], raw["action"], raw["recorded_at"]
			}
			if index, exists := staleIndex[key]; exists {
				staleItems[index] = item
			} else {
				staleIndex[key] = len(staleItems)
				staleItems = append(staleItems, item)
			}
		}
	}
	counts := map[string]int{"total": len(current), "pending": 0, "done": 0, "not_applicable": 0, "stale": len(staleItems)}
	for _, item := range current {
		counts[item["state"].(string)]++
		if item["stale"] == true {
			counts["stale"]++
		}
	}
	version := field(p.analysis, "extractor_version")
	var vn int64
	if truth(version) {
		var err error
		vn, err = strconv.ParseInt(version.Text, 10, 64)
		if version.Kind != "int" || err != nil || vn < 0 {
			return nil, unsupported("ANALYSIS_VERSION_UNSUPPORTED")
		}
	}
	notice := "Exact JD wording only. Review before applying."
	if len(current) == 0 {
		notice = "No explicit extra step detected. This is not a guarantee."
	}
	return map[string]any{"contract_version": 1, "job_id": p.id, "source_fingerprint": p.fingerprint, "extractor_version": vn,
		"items": current, "stale_items": staleItems, "counts": counts, "truncated": truth(field(p.analysis, "truncated")), "notice": notice}, nil
}

func (e *engine) add() (map[string]any, *Error) {
	p := e.prepared
	f := p.request.Fields
	if f["expected_source_fingerprint"] != p.fingerprint {
		return nil, conflict("The saved JD changed; reload it before adding this instruction")
	}
	if !category(f["category"]) {
		return nil, invalid("instruction category is invalid")
	}
	text, ok := f["selected_text"].(string)
	if !ok {
		return nil, invalid("selected_text must be a string")
	}
	text = textcompat.Strip(text)
	if text == "" {
		return nil, invalid("Select exact JD text first")
	}
	if utf8.RuneCountInString(text) > 1200 {
		return nil, invalid("Selected JD text is too long")
	}
	start := strings.Index(p.text, text)
	if start < 0 {
		return nil, conflict("Selected text is not present in the current saved JD")
	}
	fp, err := applicationanalysis.InstructionFingerprint(f["category"].(string), text)
	if err != nil {
		return nil, unsupported("EVIDENCE_UNSUPPORTED")
	}
	for _, candidate := range field(p.analysis, "items").Items {
		value := field(candidate, "fingerprint")
		if candidate.Kind == "mapping" && value.Kind == "string" && value.Text == fp {
			return nil, invalid("This exact instruction is already detected")
		}
	}
	item := map[string]any{"fingerprint": fp, "category": f["category"], "confidence": "explicit", "source_text": text,
		"line_start": strings.Count(p.text[:start], "\n") + 1, "line_end": strings.Count(p.text[:start+len(text)], "\n") + 1, "extractor_version": 0}
	if _, failure := e.review("save_manual", map[string]any{"source_fingerprint": p.fingerprint, "item": item}); failure != nil {
		return nil, failure
	}
	return e.compose()
}

func (e *engine) set() (map[string]any, *Error) {
	checklist, failure := e.compose()
	if failure != nil {
		return nil, failure
	}
	p := e.prepared
	f := p.request.Fields
	if f["expected_source_fingerprint"] != p.fingerprint {
		return nil, conflict("The saved JD changed; reload the checklist before updating it")
	}
	for _, item := range checklist["items"].([]map[string]any) {
		if item["fingerprint"] != f["instruction_fingerprint"] {
			continue
		}
		if _, failure := e.review("save_action", map[string]any{"source_fingerprint": p.fingerprint, "action": f["state"], "item": item, "origin": item["origin"]}); failure != nil {
			return nil, failure
		}
		return e.compose()
	}
	return nil, sourceError("ApplicationInstructionNotFound", "The instruction is not present in the current saved JD")
}
