package agentcontext

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// WorkbenchContextJSON is the order-preserving form for admitted JSON-compatible
// legacy stores. Store schema, job identity and fingerprint validation remain
// the read owner's responsibility. Null represents an absent store. No file is
// read here, and task matching still controls which response can be returned.
func WorkbenchContextJSON(ctx context.Context, raw json.RawMessage, task *TaskRequest) (json.RawMessage, *Error) {
	value, err := orderedWorkbenchContext(ctx, raw, true, task)
	if err != nil {
		return nil, err
	}
	return encodeOrderedContextJSON(ctx, value, false)
}

// The source uses str(value or "") for minimally validated stored prompt and
// response fields. This is display conversion, never evaluation of that text.
// Restrict it to the owner's JSON-compatible values; YAML dates, bytes, sets,
// non-finite numbers and arbitrary objects must not be silently coerced here.
func workbenchStoredText(ctx context.Context, value packetValue) (string, *Error) {
	if err := contextStopped(ctx); err != nil {
		return "", err
	}
	switch value.Kind {
	case "null":
		return "", nil
	case "bool":
		if !value.Boolean {
			return "", nil
		}
	case "int", "float":
		text, err := contextNumberText(value.Kind, value.Text)
		if err != nil {
			return "", err
		}
		zero := text == "0"
		if value.Kind == "float" {
			f, _ := strconv.ParseFloat(text, 64)
			zero = f == 0
		}
		if zero {
			return "", nil
		}
		return text, nil
	case "string":
		if !utf8.ValidString(value.Text) || len(value.Text) > 2<<20 {
			return "", failure("WORKBENCH_TEXT_LIMIT")
		}
		return value.Text, nil
	case "sequence":
		if len(value.Items) == 0 {
			return "", nil
		}
	case "mapping":
		if len(value.Entries) == 0 {
			return "", nil
		}
	}
	r := workbenchRepr{ctx: ctx}
	if err := r.value(value, 0); err != nil {
		return "", err
	}
	return r.output.String(), nil
}

type workbenchRepr struct {
	ctx    context.Context
	output strings.Builder
	nodes  int
}

func (r *workbenchRepr) append(text string) *Error {
	if err := contextStopped(r.ctx); err != nil {
		return err
	}
	if len(text) > (2<<20)-r.output.Len() {
		return failure("WORKBENCH_TEXT_LIMIT")
	}
	r.output.WriteString(text)
	return nil
}

func (r *workbenchRepr) quoted(text string) *Error {
	if !utf8.ValidString(text) {
		return failure("WORKBENCH_UNSUPPORTED")
	}
	if len(text) > (2<<20)-r.output.Len()-2 {
		return failure("WORKBENCH_TEXT_LIMIT")
	}
	quote := rune('\'')
	if strings.ContainsRune(text, '\'') && !strings.ContainsRune(text, '"') {
		quote = '"'
	}
	if err := r.append(string(quote)); err != nil {
		return err
	}
	for _, point := range text {
		piece := string(point)
		switch point {
		case '\\':
			piece = `\\`
		case '\t':
			piece = `\t`
		case '\n':
			piece = `\n`
		case '\r':
			piece = `\r`
		default:
			if point == quote {
				piece = "\\" + piece
			} else if !textcompat.IsPrintable(point) {
				width, prefix := 2, `\x`
				if point > 0xffff {
					width, prefix = 8, `\U`
				} else if point > 0xff {
					width, prefix = 4, `\u`
				}
				hex := strconv.FormatInt(int64(point), 16)
				piece = prefix + strings.Repeat("0", width-len(hex)) + hex
			}
		}
		if err := r.append(piece); err != nil {
			return err
		}
	}
	return r.append(string(quote))
}

func (r *workbenchRepr) value(v packetValue, depth int) *Error {
	if err := contextStopped(r.ctx); err != nil {
		return err
	}
	r.nodes++
	if depth > 64 || r.nodes > 200000 || len(v.Items) > 200000-r.nodes || len(v.Entries) > (200000-r.nodes)/2 {
		return failure("WORKBENCH_TEXT_LIMIT")
	}
	switch v.Kind {
	case "null":
		return r.append("None")
	case "bool":
		if v.Boolean {
			return r.append("True")
		}
		return r.append("False")
	case "int", "float":
		text, err := contextNumberText(v.Kind, v.Text)
		if err != nil {
			return err
		}
		return r.append(text)
	case "string":
		return r.quoted(v.Text)
	case "sequence":
		if err := r.append("["); err != nil {
			return err
		}
		for i, item := range v.Items {
			if i > 0 {
				if err := r.append(", "); err != nil {
					return err
				}
			}
			if err := r.value(item, depth+1); err != nil {
				return err
			}
		}
		return r.append("]")
	case "mapping":
		if err := r.append("{"); err != nil {
			return err
		}
		for i, entry := range v.Entries {
			if entry.Key.Kind != "string" {
				return failure("WORKBENCH_UNSUPPORTED")
			}
			if i > 0 {
				if err := r.append(", "); err != nil {
					return err
				}
			}
			if err := r.value(entry.Key, depth+1); err != nil {
				return err
			}
			if err := r.append(": "); err != nil {
				return err
			}
			if err := r.value(entry.Value, depth+1); err != nil {
				return err
			}
		}
		return r.append("}")
	default:
		return failure("WORKBENCH_UNSUPPORTED")
	}
}
