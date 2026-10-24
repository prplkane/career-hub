package cataloginput

// Value is a decoded scalar/collection, not a permissive external request schema.
// Kind is one of null, string, bytes, bool, int, float, date, datetime, sequence,
// mapping. Text holds Unicode scalar text, decimal integers, Python float repr,
// or ISO date/datetime text. Entries preserve post-dict insertion order.
// The caller validates node shape, canonical scalar encodings and finite size,
// depth and node budgets. Inputs must not change while validation is running.
type Value struct {
	Kind    string
	Text    string
	Boolean bool
	Bytes   []byte
	Items   []Value
	Entries []Entry
}

type Entry struct {
	Key   Value
	Value Value
}

// ValidationIssue is the privacy-limited Pydantic errors() projection. Loc owns
// its []any; components are integer positions or field/invalid-key strings.
// Input, exception context and documentation URLs are deliberately excluded.
type ValidationIssue struct {
	Type string `json:"type"`
	Loc  []any  `json:"loc"`
	Msg  string `json:"msg"`
}

// ValidationError separates supported model failures from an unsupported raw
// value representation. Unsupported must never be counted as model parity.
type ValidationError struct {
	Issues      []ValidationIssue `json:"errors"`
	Unsupported string            `json:"unsupported,omitempty"`
}

func (e *ValidationError) Error() string {
	if e.Unsupported != "" {
		return "catalog model input unsupported"
	}
	return "catalog model validation failed"
}
