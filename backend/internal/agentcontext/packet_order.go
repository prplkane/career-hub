package agentcontext

import (
	"context"
	"encoding/json"
	"strconv"

	"careerhub.local/backend/internal/cataloginput"
)

type packetValue = cataloginput.Value
type packetEntry = cataloginput.Entry

func packetField(key string, value packetValue) packetEntry {
	return packetEntry{Key: packetString(key), Value: value}
}
func packetObject(fields ...packetEntry) packetValue {
	return packetValue{Kind: "mapping", Entries: fields}
}
func packetList(items ...packetValue) packetValue {
	return packetValue{Kind: "sequence", Items: items}
}
func packetString(text string) packetValue { return packetValue{Kind: "string", Text: text} }
func packetBool(value bool) packetValue    { return packetValue{Kind: "bool", Boolean: value} }
func packetInt(value int) packetValue {
	return packetValue{Kind: "int", Text: strconv.Itoa(value)}
}
func packetNull() packetValue { return packetValue{Kind: "null"} }
func packetLookup(v packetValue, key string) (packetValue, bool) {
	for _, entry := range v.Entries {
		if entry.Key.Text == key {
			return entry.Value, true
		}
	}
	return packetNull(), false
}
func packetGet(v packetValue, key string, fallback packetValue) packetValue {
	if value, exists := packetLookup(v, key); exists {
		return value
	}
	return fallback
}
func packetPick(v packetValue, keys ...string) packetValue {
	out := packetObject()
	for _, key := range keys {
		if value, exists := packetLookup(v, key); exists {
			out.Entries = append(out.Entries, packetField(key, value))
		}
	}
	return out
}
func packetSet(v *packetValue, key string, value packetValue) {
	for i := range v.Entries {
		if v.Entries[i].Key.Text == key {
			v.Entries[i].Value = value
			return
		}
	}
	v.Entries = append(v.Entries, packetField(key, value))
}
func packetPrefix(v packetValue, maximum int) packetValue {
	if v.Kind == "sequence" {
		return packetList(v.Items[:min(maximum, len(v.Items))]...)
	}
	return v
}
func packetRecords(v packetValue, activeOnly bool) []packetValue {
	items := []packetValue{}
	for _, item := range v.Items {
		archived := packetGet(item, "archived", packetNull())
		if item.Kind == "mapping" && (!activeOnly || archived.Kind != "bool" || !archived.Boolean) {
			items = append(items, item)
		}
	}
	return items
}

// Reorder only fields that a qualified projection already retained. Original
// data cannot add keys or rows, change scalar values or undo truncation. Callers
// must pass the corresponding source row, never a name-based guessed match.
func retainPacketOrder(projected, original packetValue) packetValue {
	if projected.Kind == "mapping" && original.Kind == "mapping" {
		out := packetObject()
		seen := map[string]bool{}
		values := make(map[string]packetValue, len(projected.Entries))
		for _, entry := range projected.Entries {
			values[entry.Key.Text] = entry.Value
		}
		for _, entry := range original.Entries {
			if value, exists := values[entry.Key.Text]; exists {
				out.Entries = append(out.Entries, packetField(entry.Key.Text, retainPacketOrder(value, entry.Value)))
				seen[entry.Key.Text] = true
			}
		}
		for _, entry := range projected.Entries {
			if !seen[entry.Key.Text] {
				out.Entries = append(out.Entries, entry)
			}
		}
		return out
	}
	if projected.Kind == "sequence" && original.Kind == "sequence" {
		out := packetList()
		for i, value := range projected.Items {
			if i < len(original.Items) {
				value = retainPacketOrder(value, original.Items[i])
			}
			out.Items = append(out.Items, value)
		}
		return out
	}
	return projected
}

type packetBuilder struct {
	ctx context.Context
	err *Error
}

func (b *packetBuilder) read(raw []byte, fallback packetValue) packetValue {
	if b.err != nil || len(raw) == 0 {
		return fallback
	}
	value, err := decodeOrderedContextJSON(b.ctx, raw)
	if err != nil {
		b.err = err
	}
	return value
}
func (b *packetBuilder) typed(value any) packetValue {
	if b.err != nil {
		return packetNull()
	}
	raw, err := json.Marshal(value)
	if err != nil {
		b.err = failure("PACKET_INPUT")
		return packetNull()
	}
	return b.read(raw, packetNull())
}

type orderedPacketSources struct {
	experiences, projects, education, profile, visa, skills, style packetValue
}

func (b *packetBuilder) sources(s ModelSources) orderedPacketSources {
	return orderedPacketSources{
		b.read(s.Experiences, packetNull()), b.read(s.Projects, packetNull()),
		b.read(s.Education, packetNull()), b.read(s.Profile, packetNull()),
		b.read(s.Visa, packetNull()), b.read(s.Skills, packetNull()), b.read(s.StyleRules, packetNull()),
	}
}

// Semantic selection, missing-reference handling and allowlists are provided by
// BuildEvidenceContext. Restore field order from the SAME observed source bytes,
// rather than reimplementing ranking or reopening files after selection.
func orderEvidencePacket(matrix, model, worksheet, card, assessment packetValue, sources orderedPacketSources) (packetValue, packetValue, packetValue, *Error) {
	bad := func() (packetValue, packetValue, packetValue, *Error) {
		return packetValue{}, packetValue{}, packetValue{}, failure("PACKET_PROJECTION_IDENTITY")
	}
	catalog := map[string]packetValue{}
	for _, list := range []packetValue{sources.experiences, sources.projects, packetGet(sources.education, "certifications", packetList())} {
		for _, row := range packetRecords(list, true) {
			catalog[packetGet(row, "id", packetNull()).Text] = row
		}
	}
	direct := packetGet(model, "candidate_direct_evidence", packetList())
	for i, item := range direct.Items {
		original, exists := catalog[packetGet(item, "id", packetNull()).Text]
		if !exists {
			return bad()
		}
		kind := packetGet(item, "evidence_type", packetNull()).Text
		keys := []string{"id", "evidence_type", "company", "role", "dates", "category", "technologies", "proof_points"}
		if kind == "project" {
			keys = []string{"id", "evidence_type", "name", "category", "summary", "technologies", "proof_points", "related_experience"}
		} else if kind == "certification" {
			keys = []string{"id", "evidence_type", "name", "credential", "completed", "proof_points"}
		}
		item = packetPick(item, keys...)
		for j, entry := range item.Entries {
			if entry.Key.Text != "evidence_type" {
				item.Entries[j].Value = retainPacketOrder(entry.Value, packetGet(original, entry.Key.Text, packetNull()))
			}
		}
		direct.Items[i] = item
	}
	packetSet(&model, "candidate_direct_evidence", direct)
	packetSet(&model, "job", packetPick(card, "id", "company", "title", "location", "remote_policy", "seniority", "responsibilities", "visa_signals"))
	packetSet(&model, "unresolved_decisions", packetPick(assessment, "eligibility_blockers", "requirement_gaps", "missing_preferred", "manual_review"))
	packetSet(&model, "applicable_style_rules", packetPick(sources.style, "resume_voice", "bullet_rules", "selection_rules", "forbidden"))
	candidate := packetGet(model, "candidate", packetObject())
	candidate = packetPick(candidate, "current_location", "target_location", "location_flexibility", "work_authorization", "requires_future_sponsorship", "education")
	for i, entry := range candidate.Entries {
		original := sources.profile
		if entry.Key.Text == "work_authorization" || entry.Key.Text == "requires_future_sponsorship" {
			original = sources.visa
		}
		if entry.Key.Text != "education" {
			candidate.Entries[i].Value = retainPacketOrder(entry.Value, packetGet(original, entry.Key.Text, packetNull()))
			continue
		}
		schools := packetRecords(packetGet(sources.education, "schools", packetList()), true)
		if len(schools) != len(entry.Value.Items) {
			return bad()
		}
		for j, row := range entry.Value.Items {
			row = packetPick(row, "id", "institution", "degree", "major", "location", "dates", "graduation_date", "gpa", "status", "honors")
			for k, field := range row.Entries {
				row.Entries[k].Value = retainPacketOrder(field.Value, packetGet(schools[j], field.Key.Text, packetNull()))
			}
			candidate.Entries[i].Value.Items[j] = row
		}
	}
	packetSet(&model, "candidate", candidate)
	// Classified provenance is the only arbitrary nested source value in matrix
	// rows. Matrix construction follows the same classified list in source order.
	digest := packetGet(card, "digest", packetObject())
	classified := packetGet(digest, "classified_requirements", packetList())
	rows := packetGet(matrix, "requirements", packetList())
	index := 0
	for i, row := range rows.Items {
		if evidence, exists := packetLookup(row, "source_evidence"); exists {
			if index >= len(classified.Items) {
				return bad()
			}
			packetSet(&row, "source_evidence", retainPacketOrder(evidence, packetGet(classified.Items[index], "evidence", packetList())))
			index++
			rows.Items[i] = row
		}
	}
	packetSet(&matrix, "requirements", rows)
	experiences := packetRecords(sources.experiences, true)
	decisions := packetGet(worksheet, "experience_candidates", packetList())
	if len(experiences) != len(decisions.Items) {
		return bad()
	}
	for i, row := range decisions.Items {
		for _, key := range []string{"company", "role", "dates", "category", "evidence_preview"} {
			sourceKey := key
			if key == "evidence_preview" {
				sourceKey = "proof_points"
			}
			packetSet(&row, key, retainPacketOrder(packetGet(row, key, packetNull()), packetGet(experiences[i], sourceKey, packetNull())))
		}
		decisions.Items[i] = row
	}
	packetSet(&worksheet, "experience_candidates", decisions)
	return matrix, model, worksheet, nil
}
