package applicationcontacts

import "slices"

func (e *engine) exact(channels []channel) (map[string][]string, []string, *Error) {
	out := map[string][]string{}
	unique := map[string]bool{}
	for _, c := range channels {
		if c.Kind == "phone" {
			continue
		}
		rows, p := e.query("SELECT "+textSQL("contact_id")+" FROM application_contact_channels WHERE channel_type=? AND normalized_value=? ORDER BY contact_id LIMIT 10001", maxRows, c.Kind, c.Normalized)
		if p != nil {
			return nil, nil, p
		}
		if len(rows) == 0 {
			continue
		}
		ids := []string{}
		for _, row := range rows {
			s, p := stringRow(row)
			if p != nil {
				return nil, nil, p
			}
			ids = append(ids, s[0])
			unique[s[0]] = true
		}
		out[c.Kind+":"+c.Normalized] = ids
	}
	ids := []string{}
	for key := range unique {
		ids = append(ids, key)
	}
	slices.Sort(ids)
	return out, ids, nil
}
func (e *engine) potential(f map[string]string, excluding string) ([]map[string]any, *Error) {
	rows, p := e.query("SELECT "+columns([]string{"contact_id", "name", "role", "company"})+",revision FROM application_contacts WHERE archived_at IS NULL AND contact_id<>? ORDER BY updated_at DESC,contact_id LIMIT 500", 500, excluding)
	if p != nil {
		return nil, p
	}
	out := []map[string]any{}
	for _, row := range rows {
		s, p := stringRow(row[:4])
		if p != nil {
			return nil, p
		}
		revision, ok := row[4].(int64)
		if !ok || revision <= 0 {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		fields := []string{}
		for i, key := range []string{"name", "role", "company"} {
			if len([]rune(s[i+1])) > factLimit(key) {
				return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
			}
			threshold := .95
			if key == "name" {
				threshold = .92
			}
			matched, p := similar(e.ctx, f[key], s[i+1], threshold, &e.work)
			if p != nil {
				return nil, p
			}
			if matched {
				fields = append(fields, key)
			}
		}
		if slices.Contains(fields, "name") || slices.Contains(fields, "role") && slices.Contains(fields, "company") {
			out = append(out, map[string]any{"contact_id": s[0], "name": s[1], "role": s[2], "company": s[3], "revision": revision, "matched_fields": fields})
			if len(out) == 10 {
				break
			}
		}
	}
	return out, nil
}
func (e *engine) find(m map[string]any) (any, *Error) {
	f, p := facts(m)
	if p != nil {
		return nil, p
	}
	channels, p := normalizeChannels(value(m, "channels", []any{}))
	if p != nil {
		return nil, p
	}
	exact, ids, p := e.exact(channels)
	if p != nil {
		return nil, p
	}
	items := []map[string]any{}
	for _, key := range ids {
		item, p := e.record(key)
		if p != nil {
			return nil, p
		}
		items = append(items, item)
	}
	potential, p := e.potential(f, "")
	if p != nil {
		return nil, p
	}
	return map[string]any{"contract_version": ContractVersion, "exact_matches": items, "exact_channels": exact, "potential_duplicates": potential}, nil
}
func (e *engine) findProvenance(m map[string]any) (any, *Error) {
	reference, p := text(m["source_reference"], "source_reference", 256, false, false)
	if p != nil {
		return nil, p
	}
	hash, p := text(m["source_content_sha256"], "source_content_sha256", 64, false, false)
	if p != nil {
		return nil, p
	}
	hash = fold(hash)
	if !hexadecimal(hash, 64) {
		return nil, invalid("source_content_sha256 is invalid")
	}
	key, p := e.proofOwner(map[string]any{"origin": "jd_candidate", "source_reference": reference, "source_content_sha256": hash})
	if p != nil || key == "" {
		return nil, p
	}
	return e.record(key)
}
func (e *engine) list(flag any) (any, *Error) {
	include, ok := flag.(bool)
	if !ok {
		return nil, invalid("include_archived must be a boolean")
	}
	where := ""
	if !include {
		where = " WHERE archived_at IS NULL"
	}
	rows, p := e.query("SELECT "+columns(baseFields)+",revision FROM application_contacts"+where+" ORDER BY archived_at IS NOT NULL,name,company,contact_id LIMIT 10001", maxRows)
	if p != nil {
		return nil, p
	}
	items := []map[string]any{}
	for _, raw := range rows {
		row, p := base(raw)
		if p != nil {
			return nil, p
		}
		item, p := e.project(row)
		if p != nil {
			return nil, p
		}
		items = append(items, item)
	}
	return map[string]any{"contract_version": ContractVersion, "include_archived": include, "items": items, "total": len(items)}, nil
}
