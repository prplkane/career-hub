package vaultread

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func displaySamples() []string {
	samples := []string{
		"null", "false", "true", "0", "-0", "0.0", "-0.0", ".nan", ".inf", "-.inf",
		"123456789012345678901234567890", "1.2e+30", "'  '", "''", "[]", "{}",
		"[null, false, true, 1, -2.5, .nan]", "{a: b, false: x, 0: y, 'false': z}",
		"[2026-09-08]", "2026-09-08", "0001-01-01",
		"2026-09-08T12:00:00Z", "[2026-09-08T12:00:00Z]",
		"[2026-09-08T00:00:00, 2026-09-08T12:34:56.000123+05:30, 2026-09-08T12:00:00-23:59]",
		"[2026-09-08T12:00:00.123456789-00:00]",
		"!!binary ''", "!!binary Jw==", "!!binary JyI=",
	}
	var allBytes []byte
	for value := 0; value <= 255; value++ {
		allBytes = append(allBytes, byte(value))
	}
	samples = append(samples, "!!binary "+base64.StdEncoding.EncodeToString(allBytes))
	samples = append(samples, "[!!binary "+base64.StdEncoding.EncodeToString(allBytes)+"]")
	for _, text := range []string{
		"'quoted'", `"quoted"`, "'\"both", "line\n\t\r\x00\x1c", "\u00a0\u00ad\u200b\u2028",
		"Ω é \u0301 中文 😀", "\u1c89\U0001faea\U00031350", "\uffff\U000fffff",
	} {
		// YAML forbids some literal code points but admits their escaped form.
		raw := strconv.QuoteToASCII(text)
		samples = append(samples, raw, "["+raw+"]")
	}
	// A deterministic scalar corpus checks raw str versus nested repr escaping.
	for index := 0; index < 120; index++ {
		point := rune((index*7919 + 127) % 0x110000)
		if point >= 0xd800 && point <= 0xdfff {
			point = 0x20
		}
		raw := strconv.QuoteToASCII("a'" + string(point) + "\"\t")
		samples = append(samples, raw, "{key: ["+raw+", 1]}")
	}
	return samples
}

func TestDisplayTruthinessAndSafeBounds(t *testing.T) {
	for index, sample := range displaySamples() {
		value, failure := cataloginput.ParseYAML([]byte(sample))
		if failure != nil {
			t.Fatalf("synthetic display fixture %d was not admitted: %s", index, failure.Code)
		}
		if _, failure := displayText(context.Background(), "synthetic", value); failure != nil {
			t.Fatal("admitted display fixture failed")
		}
	}
	for _, value := range []cataloginput.Value{
		{Kind: "string", Text: strings.Repeat("x", MaxProfileBytes+1)},
		{Kind: "sequence", Items: []cataloginput.Value{{Kind: "string", Text: strings.Repeat("\x00", MaxProfileBytes/4)}}},
	} {
		text, failure := displayText(context.Background(), "synthetic", value)
		if text != "" || failure == nil || failure.Code != "VAULT_PROJECTION_LIMIT" {
			t.Fatal("oversized display output was not refused")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if text, failure := displayText(ctx, "synthetic", cataloginput.Value{Kind: "string", Text: "private"}); text != "" || failure == nil || failure.Code != "VAULT_READ_CANCELED" {
		t.Fatal("canceled display leaked text")
	}
	if text, failure := displayText(context.Background(), "synthetic", cataloginput.Value{Kind: "unsupported"}); text != "" || failure == nil || failure.Code != "VAULT_PROJECTION_UNSUPPORTED" {
		t.Fatal("unsupported display kind was guessed")
	}
}
