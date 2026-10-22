package vaultread

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

// Python display conversion is separate from JSON serialization. Only bounded
// Values from ParseYAML are admitted here; no arbitrary objects, cycles or custom
// conversion methods are supported. Display output never changes stored values.
func truthy(value cataloginput.Value) bool {
	switch value.Kind {
	case "null":
		return false
	case "bool":
		return value.Boolean
	case "int":
		return value.Text != "0"
	case "float":
		return value.Text != "0.0" && value.Text != "-0.0"
	case "string":
		return value.Text != ""
	case "bytes":
		return len(value.Bytes) != 0
	case "mapping":
		return len(value.Entries) != 0
	case "sequence":
		return len(value.Items) != 0
	}
	return true
}

type displayWriter struct {
	ctx     context.Context
	source  string
	out     strings.Builder
	failure *Error
}

func displayText(ctx context.Context, source string, value cataloginput.Value) (string, *Error) {
	w := displayWriter{ctx: ctx, source: source}
	w.value(value, false)
	if w.failure != nil {
		return "", w.failure
	}
	return w.out.String(), nil
}

func (w *displayWriter) add(text string) {
	if w.failure != nil {
		return
	}
	if w.ctx == nil || w.ctx.Err() != nil {
		w.failure = canceled()
		return
	}
	if len(text) > MaxProfileBytes-w.out.Len() {
		w.failure = &Error{Stage: "projection", Source: w.source, Code: "VAULT_PROJECTION_LIMIT"}
		return
	}
	w.out.WriteString(text)
}

func (w *displayWriter) quoted(value string) {
	quote := "'"
	if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
		quote = `"`
	}
	w.add(quote)
	for _, point := range value {
		if w.failure != nil {
			return
		}
		switch point {
		case '\\':
			w.add(`\\`)
		case '\n':
			w.add(`\n`)
		case '\r':
			w.add(`\r`)
		case '\t':
			w.add(`\t`)
		default:
			if string(point) == quote {
				w.add(`\` + quote)
			} else if textcompat.IsPrintable(point) {
				w.add(string(point))
			} else if point <= 0xff {
				w.add(fmt.Sprintf(`\x%02x`, point))
			} else if point <= 0xffff {
				w.add(fmt.Sprintf(`\u%04x`, point))
			} else {
				w.add(fmt.Sprintf(`\U%08x`, point))
			}
		}
	}
	w.add(quote)
}

func (w *displayWriter) bytes(value []byte) {
	quote := "'"
	if strings.Contains(string(value), "'") && !strings.Contains(string(value), `"`) {
		quote = `"`
	}
	w.add("b" + quote)
	for _, point := range value {
		if w.failure != nil {
			return
		}
		switch point {
		case '\\':
			w.add(`\\`)
		case '\n':
			w.add(`\n`)
		case '\r':
			w.add(`\r`)
		case '\t':
			w.add(`\t`)
		default:
			if string(point) == quote {
				w.add(`\` + quote)
			} else if point >= 0x20 && point <= 0x7e {
				w.add(string(point))
			} else {
				w.add(fmt.Sprintf(`\x%02x`, point))
			}
		}
	}
	w.add(quote)
}

func decimalPart(text string) int {
	number, _ := strconv.Atoi(text) // Canonical date/datetime text from the decoder.
	return number
}

func (w *displayWriter) datetime(value cataloginput.Value, repr bool) {
	if !repr {
		w.add(strings.Replace(value.Text, "T", " ", 1))
		return
	}
	text := value.Text
	year, month, day := decimalPart(text[:4]), decimalPart(text[5:7]), decimalPart(text[8:10])
	if value.Kind == "date" {
		w.add(fmt.Sprintf("datetime.date(%d, %d, %d)", year, month, day))
		return
	}
	hour, minute, second := decimalPart(text[11:13]), decimalPart(text[14:16]), decimalPart(text[17:19])
	w.add(fmt.Sprintf("datetime.datetime(%d, %d, %d, %d, %d", year, month, day, hour, minute))
	tail, micros := text[19:], 0
	if strings.HasPrefix(tail, ".") {
		micros = decimalPart(tail[1:7])
		tail = tail[7:]
	}
	if second != 0 || micros != 0 {
		w.add(fmt.Sprintf(", %d", second))
	}
	if micros != 0 {
		w.add(fmt.Sprintf(", %d", micros))
	}
	if tail != "" {
		seconds := (decimalPart(tail[1:3])*60 + decimalPart(tail[4:6])) * 60
		if tail[0] == '-' {
			seconds = -seconds
		}
		if seconds == 0 {
			w.add(", tzinfo=datetime.timezone.utc")
		} else {
			w.add(", tzinfo=datetime.timezone(datetime.timedelta(")
			if seconds < 0 {
				w.add(fmt.Sprintf("days=-1, seconds=%d", 86400+seconds))
			} else {
				w.add(fmt.Sprintf("seconds=%d", seconds))
			}
			w.add("))")
		}
	}
	w.add(")")
}

func (w *displayWriter) value(value cataloginput.Value, repr bool) {
	if w.failure != nil {
		return
	}
	switch value.Kind {
	case "string":
		if repr {
			w.quoted(value.Text)
		} else {
			w.add(value.Text)
		}
	case "bytes":
		w.bytes(value.Bytes)
	case "date", "datetime":
		w.datetime(value, repr)
	case "null":
		w.add("None")
	case "bool":
		if value.Boolean {
			w.add("True")
		} else {
			w.add("False")
		}
	case "int", "float":
		w.add(value.Text)
	case "sequence":
		w.add("[")
		for index, item := range value.Items {
			if w.failure != nil {
				return
			}
			if index != 0 {
				w.add(", ")
			}
			w.value(item, true)
		}
		w.add("]")
	case "mapping":
		w.add("{")
		for index, entry := range value.Entries {
			if w.failure != nil {
				return
			}
			if index != 0 {
				w.add(", ")
			}
			w.value(entry.Key, true)
			w.add(": ")
			w.value(entry.Value, true)
		}
		w.add("}")
	default:
		w.failure = &Error{Stage: "projection", Source: w.source, Code: "VAULT_PROJECTION_UNSUPPORTED"}
	}
}
