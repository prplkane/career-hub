package applicationcontacts

import "slices"

func (e *engine) result(key, outcome string, extra map[string]any) (any, *Error) {
	record, p := e.record(key)
	if p != nil {
		return nil, p
	}
	if extra == nil {
		extra = map[string]any{}
	}
	extra["outcome"], extra["contact"] = outcome, record
	return extra, nil
}
func (e *engine) create(m map[string]any) (any, *Error) {
	f, p := facts(m)
	if p != nil {
		return nil, p
	}
	channels, p := normalizeChannels(value(m, "channels", []any{}))
	if p != nil {
		return nil, p
	}
	if f["name"] == "" && len(channels) == 0 {
		return nil, invalid("A contact requires a name or exact channel")
	}
	jobs, p := links(value(m, "job_ids", []any{}), "job_ids")
	if p != nil {
		return nil, p
	}
	discoveries, p := links(value(m, "discovery_ids", []any{}), "discovery_ids")
	if p != nil {
		return nil, p
	}
	u, p := e.uuid()
	if p != nil {
		return nil, p
	}
	key := "contact_" + u
	proof, p := provenance(m["provenance"], "manual-create:"+key, stated(f, channels))
	if p != nil {
		return nil, p
	}
	pj, pd := proofSources(proof)
	if p = e.requireSources(append(slices.Clone(jobs), pj...), append(slices.Clone(discoveries), pd...)); p != nil {
		return nil, p
	}
	owner, p := e.proofOwner(proof)
	if p != nil {
		return nil, p
	}
	empty := map[string]any{"exact_channels": map[string][]string{}, "potential_duplicates": []map[string]any{}}
	if owner != "" {
		return e.result(owner, "provenance_match", empty)
	}
	exact, ids, p := e.exact(channels)
	if p != nil {
		return nil, p
	}
	if len(ids) > 1 {
		return nil, problem("EXACT_MATCH_AMBIGUOUS", "Exact channels belong to different existing contacts")
	}
	if len(ids) > 0 {
		empty["exact_channels"] = exact
		return e.result(ids[0], "exact_match", empty)
	}
	potential, p := e.potential(f, "")
	if p != nil {
		return nil, p
	}
	if p = e.collision("application_contacts", "contact_id", key); p != nil {
		return nil, p
	}
	identity := channel{}
	if len(channels) > 0 {
		identity = channels[0]
	}
	e.creating = true
	if _, p = e.exec("INSERT INTO application_contacts(contact_id,name,role,company,context,identity_channel_type,identity_channel_normalized,archived_at,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,NULL,1,?,?)", key, f["name"], f["role"], f["company"], f["context"], identity.Kind, identity.Normalized, e.clock, e.clock); p != nil {
		return nil, p
	}
	for _, c := range channels {
		u, p := e.uuid()
		if p != nil {
			return nil, p
		}
		channelID := "contact_channel_" + u
		if p = e.collision("application_contact_channels", "channel_id", channelID); p != nil {
			return nil, p
		}
		if _, p = e.exec("INSERT INTO application_contact_channels(channel_id,contact_id,channel_type,exact_value,normalized_value,created_at) VALUES(?,?,?,?,?,?)", channelID, key, c.Kind, c.Value, c.Normalized, e.clock); p != nil {
			return nil, p
		}
	}
	if p = e.insertProof(key, proof); p != nil {
		return nil, p
	}
	if p = e.insertLinks(key, jobs, discoveries); p != nil {
		return nil, p
	}
	empty["potential_duplicates"] = potential
	return e.result(key, "created", empty)
}
func (e *engine) insertLinks(key string, jobs, discoveries []string) *Error {
	for i, items := range [][]string{jobs, discoveries} {
		kind := "job"
		if i == 1 {
			kind = "discovery"
		}
		table, _ := linkTables(kind)
		for _, item := range items {
			if _, p := e.exec("INSERT OR IGNORE INTO "+table+"(contact_id,"+kind+"_id,created_at) VALUES(?,?,?)", key, item, e.clock); p != nil {
				return p
			}
		}
	}
	return nil
}
func fieldsFromRow(row map[string]any) map[string]string {
	out := map[string]string{}
	for _, f := range factNames {
		out[f] = row[f].(string)
	}
	return out
}
func (e *engine) update(m map[string]any, order []string) (any, *Error) {
	key, p := id(m["contact_id"])
	if p != nil {
		return nil, p
	}
	rev, p := revision(m["expected_revision"])
	if p != nil {
		return nil, p
	}
	updates, ok := m["updates"].(map[string]any)
	if !ok || len(updates) == 0 {
		return nil, invalid("updates must contain contact fact fields only")
	}
	for k := range updates {
		if !slices.Contains(factNames, k) {
			return nil, invalid("updates must contain contact fact fields only")
		}
	}
	if len(order) == 0 {
		for _, f := range factNames {
			if _, ok := updates[f]; ok {
				order = append(order, f)
			}
		}
	} else {
		seen := map[string]bool{}
		for _, f := range order {
			if _, ok := updates[f]; !ok || seen[f] {
				return nil, unsupported("UPDATE_ORDER_INVALID")
			}
			seen[f] = true
		}
		if len(seen) != len(updates) {
			return nil, unsupported("UPDATE_ORDER_INVALID")
		}
	}
	row, p := e.selectContact(key)
	if p != nil {
		return nil, p
	}
	if row["revision"] != rev {
		return nil, stale()
	}
	if row["archived_at"] != nil {
		return nil, problem("ARCHIVED", "Restore this contact before editing it")
	}
	f := fieldsFromRow(row)
	changed := false
	for _, field := range order {
		s, p := text(updates[field], "updates."+field, factLimit(field), true, false)
		if p != nil {
			return nil, p
		}
		changed = changed || f[field] != s
		f[field] = s
	}
	if f["name"] == "" && row["identity_channel_normalized"] == "" {
		return nil, invalid("A contact requires a name or exact channel")
	}
	if !changed {
		record, p := e.project(row)
		if p != nil {
			return nil, p
		}
		potential, p := e.potential(f, key)
		if p != nil {
			return nil, p
		}
		return map[string]any{"outcome": "unchanged", "contact": record, "potential_duplicates": potential}, nil
	}
	_, channels, p := e.channels(key)
	if p != nil {
		return nil, p
	}
	u, p := e.uuid()
	if p != nil {
		return nil, p
	}
	proof, p := provenance(m["provenance"], "manual-update:"+u, stated(f, channels))
	if p != nil {
		return nil, p
	}
	if p = e.requireSources(proofSources(proof)); p != nil {
		return nil, p
	}
	if p = e.bump(key, rev, "name=?,role=?,company=?,context=?,", f["name"], f["role"], f["company"], f["context"]); p != nil {
		return nil, p
	}
	if p = e.insertProof(key, proof); p != nil {
		return nil, p
	}
	potential, p := e.potential(f, key)
	if p != nil {
		return nil, p
	}
	return e.result(key, "updated", map[string]any{"potential_duplicates": potential})
}
func (e *engine) missingLinks(key string, jobs, discoveries []string) (bool, *Error) {
	for i, items := range [][]string{jobs, discoveries} {
		kind := "job"
		if i == 1 {
			kind = "discovery"
		}
		table, _ := linkTables(kind)
		for _, item := range items {
			rows, p := e.query("SELECT 1 FROM "+table+" WHERE contact_id=? AND "+kind+"_id=? LIMIT 1", 1, key, item)
			if p != nil {
				return false, p
			}
			if len(rows) == 0 {
				return true, nil
			}
		}
	}
	return false, nil
}
func (e *engine) link(m map[string]any, unlink bool) (any, *Error) {
	key, p := id(m["contact_id"])
	if p != nil {
		return nil, p
	}
	rev, p := revision(m["expected_revision"])
	if p != nil {
		return nil, p
	}
	jobs, p := links(value(m, "job_ids", []any{}), "job_ids")
	if p != nil {
		return nil, p
	}
	discoveries, p := links(value(m, "discovery_ids", []any{}), "discovery_ids")
	if p != nil {
		return nil, p
	}
	if len(jobs)+len(discoveries) == 0 {
		if unlink {
			return nil, invalid("unlink_contact requires at least one link")
		}
		if m["provenance"] == nil {
			return nil, invalid("link_contact requires a link or provenance")
		}
	}
	row, p := e.selectContact(key)
	if p != nil {
		return nil, p
	}
	var proof map[string]any
	if !unlink && m["provenance"] != nil {
		current, p := e.project(row)
		if p != nil {
			return nil, p
		}
		u, p := e.uuid()
		if p != nil {
			return nil, p
		}
		cs := []any{}
		for _, c := range current["channels"].([]map[string]any) {
			cs = append(cs, map[string]any{"kind": c["kind"], "value": c["value"]})
		}
		fallback := map[string]any{"channels": cs}
		for _, f := range factNames {
			fallback[f] = row[f]
		}
		proof, p = provenance(m["provenance"], "manual-link:"+u, fallback)
		if p != nil {
			return nil, p
		}
		owner, p := e.proofOwner(proof)
		if p != nil {
			return nil, p
		}
		if owner != "" && owner != key {
			return nil, proofConflict()
		}
		if owner == key {
			missing, p := e.missingLinks(key, jobs, discoveries)
			if p != nil {
				return nil, p
			}
			if !missing {
				return map[string]any{"outcome": "unchanged", "contact": current}, nil
			}
		}
	}
	if row["revision"] != rev {
		return nil, stale()
	}
	if unlink {
		for i, items := range [][]string{jobs, discoveries} {
			kind := "job"
			if i == 1 {
				kind = "discovery"
			}
			table, _ := linkTables(kind)
			for _, item := range items {
				if _, p = e.exec("DELETE FROM "+table+" WHERE contact_id=? AND "+kind+"_id=?", key, item); p != nil {
					return nil, p
				}
			}
		}
	} else {
		if row["archived_at"] != nil {
			return nil, problem("ARCHIVED", "Restore this contact before linking it")
		}
		if p = e.requireSources(jobs, discoveries); p != nil {
			return nil, p
		}
		if p = e.insertLinks(key, jobs, discoveries); p != nil {
			return nil, p
		}
		if proof != nil {
			if p = e.requireSources(proofSources(proof)); p != nil {
				return nil, p
			}
			if p = e.insertProof(key, proof); p != nil {
				return nil, p
			}
		}
	}
	if !e.changed {
		return e.result(key, "unchanged", nil)
	}
	if p = e.bump(key, rev, ""); p != nil {
		return nil, p
	}
	outcome := "linked"
	if unlink {
		outcome = "unlinked"
	}
	return e.result(key, outcome, nil)
}
func (e *engine) archive(m map[string]any, archived bool) (any, *Error) {
	key, p := id(m["contact_id"])
	if p != nil {
		return nil, p
	}
	rev, p := revision(m["expected_revision"])
	if p != nil {
		return nil, p
	}
	row, p := e.selectContact(key)
	if p != nil {
		return nil, p
	}
	if row["revision"] != rev {
		return nil, stale()
	}
	if (row["archived_at"] != nil) == archived {
		return e.result(key, "unchanged", nil)
	}
	var stamp any
	outcome := "restored"
	if archived {
		stamp = e.clock
		outcome = "archived"
	}
	if p = e.bump(key, rev, "archived_at=?,", stamp); p != nil {
		return nil, p
	}
	return e.result(key, outcome, nil)
}
