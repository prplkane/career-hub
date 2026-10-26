package applicationreview

import (
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// This is storage validation, not proof that the excerpt belongs to a saved JD.
// The higher-level checklist service must retain its source/revision checks.
func instructionSnapshot(item any, origin any) (map[string]any, *Error) {
	source, ok := origin.(string)
	if !ok || !oneOf(source, "detected", "manual") {
		return nil, invalid("instruction origin is invalid")
	}
	f, ok := item.(map[string]any)
	if !ok {
		return nil, unsupported("INSTRUCTION_INPUT_UNSUPPORTED")
	}
	hash, p := sha(f["fingerprint"], "instruction fingerprint", false)
	if p != nil {
		return nil, p
	}
	category, ok := f["category"].(string)
	if !ok || !oneOf(category, "email_destination", "exact_phrase", "required_artifact", "cover_letter_format", "external_step", "deadline") {
		return nil, invalid("instruction category is invalid")
	}
	confidence, ok := f["confidence"].(string)
	if !ok || !oneOf(confidence, "explicit", "review") {
		return nil, invalid("instruction confidence is invalid")
	}
	text, ok := f["source_text"].(string)
	if !ok || textcompat.Strip(text) == "" {
		return nil, invalid("instruction source_text must not be empty")
	}
	if utf8.RuneCountInString(text) > 1200 {
		return nil, invalid("instruction source_text exceeds the evidence limit")
	}
	start, p := integer(f["line_start"], 1, "instruction line_start must be positive")
	if p != nil {
		return nil, p
	}
	end, p := integer(f["line_end"], 1, "instruction line_end must follow line_start")
	if p != nil {
		return nil, p
	}
	if end.Cmp(start) < 0 {
		return nil, invalid("instruction line_end must follow line_start")
	}
	version, p := integer(f["extractor_version"], 0, "instruction extractor_version must be non-negative")
	if p != nil {
		return nil, p
	}
	return map[string]any{"fingerprint": hash, "category": category, "confidence": confidence, "origin": source, "source_text": text, "line_start": start, "line_end": end, "extractor_version": version}, nil
}
func instructionColumns(manual bool) string {
	name := "event_id"
	stamp := "recorded_at"
	if manual {
		name = "item_id"
		stamp = "created_at"
	}
	out := number(name) + " AS " + name + "," + text("instruction_fingerprint") + " AS fingerprint," + cols("source_fingerprint", "category", "confidence", "source_text", stamp) + "," + number("line_start") + " AS line_start," + number("line_end") + " AS line_end," + number("extractor_version") + " AS extractor_version"
	if !manual {
		out += "," + cols("action", "origin")
	}
	return out
}
func (e *engine) instructions(op string, id any) (any, *Error) {
	if p := e.requireJob(id); p != nil {
		return nil, p
	}
	manual := op == "list_manual"
	table, order := "application_instruction_action_events", "event_id DESC"
	if manual {
		table, order = "application_instruction_manual_items", "item_id"
	}
	rows, p := e.query("SELECT "+instructionColumns(manual)+" FROM "+table+" WHERE job_id=? ORDER BY "+order, id)
	if p != nil {
		return nil, p
	}
	if manual {
		for _, r := range rows {
			r["origin"] = "manual"
		}
	}
	return rows, nil
}
func (e *engine) saveInstruction(r Request) (any, *Error) {
	f := r.Fields
	source, p := sha(f["source_fingerprint"], "source fingerprint", false)
	if p != nil {
		return nil, p
	}
	origin := f["origin"]
	action := ""
	manual := r.Operation == "save_manual"
	if manual {
		origin = "manual"
	} else {
		var ok bool
		action, ok = f["action"].(string)
		if !ok || !oneOf(action, "pending", "done", "not_applicable") {
			return nil, invalid("instruction action must be pending, done, or not_applicable")
		}
	}
	item, p := instructionSnapshot(f["item"], origin)
	if p != nil {
		return nil, p
	}
	if p = e.requireJob(f["job_id"]); p != nil {
		return nil, p
	}
	if manual {
		_, p = e.exec("INSERT OR IGNORE INTO application_instruction_manual_items(job_id,instruction_fingerprint,source_fingerprint,category,confidence,source_text,line_start,line_end,extractor_version,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)", f["job_id"], item["fingerprint"], source, item["category"], item["confidence"], item["source_text"], item["line_start"], item["line_end"], item["extractor_version"], e.clock)
		if p != nil {
			return nil, p
		}
		out, p := e.one("SELECT "+instructionColumns(true)+" FROM application_instruction_manual_items WHERE job_id=? AND instruction_fingerprint=? AND source_fingerprint=?", f["job_id"], item["fingerprint"], source)
		if p != nil {
			return nil, p
		}
		if out == nil {
			return nil, invalid("Manual instruction could not be reloaded")
		}
		out["origin"] = "manual"
		return out, nil
	}
	id, p := e.exec("INSERT INTO application_instruction_action_events(job_id,instruction_fingerprint,source_fingerprint,action,category,confidence,origin,source_text,line_start,line_end,extractor_version,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", f["job_id"], item["fingerprint"], source, action, item["category"], item["confidence"], item["origin"], item["source_text"], item["line_start"], item["line_end"], item["extractor_version"], e.clock)
	if p != nil {
		return nil, p
	}
	item["event_id"] = id
	item["source_fingerprint"] = source
	item["action"] = action
	item["recorded_at"] = e.clock
	return item, nil
}
