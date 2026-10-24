package emailmime

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
)

func TestBodyReadBudget(t *testing.T) {
	st := &state{remainingBodyBytes: 5}
	for _, body := range []string{"ab", "cde", ""} {
		got, err := st.readBody(strings.NewReader(body))
		if err != nil || string(got) != body {
			t.Fatalf("exact cumulative read: %q %v", got, err)
		}
	}
	reader := strings.NewReader("xyz")
	if got, err := st.readBody(reader); got != nil || !errors.Is(err, ErrParseBudget) || reader.Len() != 2 {
		t.Fatalf("overflow did not stop after one-byte probe: %q %v unread=%d", got, err, reader.Len())
	}
	if _, err := (&state{remainingBodyBytes: 3}).readBody(strings.NewReader("xyz")); err != nil {
		t.Fatal("independent budget was affected", err)
	}
}

func TestEveryBodyCopyConsumesBudget(t *testing.T) {
	for _, transfer := range []string{"base64", "quoted-printable"} {
		t.Run(transfer, func(t *testing.T) {
			raw := []byte("abc")
			if transfer == "base64" {
				raw = []byte(base64.StdEncoding.EncodeToString(raw))
			}
			for _, budget := range []int{2, 3} {
				st := &state{remainingBodyBytes: budget}
				got, err := decodeTransfer(transfer, raw, st)
				if budget == 2 && (got != nil || !errors.Is(err, ErrParseBudget)) {
					t.Fatalf("transfer overflow: %q %v", got, err)
				}
				if budget == 3 && (err != nil || string(got) != "abc" || st.remainingBodyBytes != 0) {
					t.Fatalf("transfer exact limit: %q %v", got, err)
				}
			}
		})
	}
	raw := []byte("abc")
	got, err := decodeTransfer("8bit", raw, &state{})
	if err != nil || &got[0] != &raw[0] {
		t.Fatal("passthrough must alias existing bytes without consuming read budget")
	}
	message := []byte("Content-Type: text/plain\r\n\r\nabc")
	if _, body, err := readMessage(message, &state{remainingBodyBytes: 2}); body != nil || !errors.Is(err, ErrParseBudget) {
		t.Fatalf("message body escaped budget: %q %v", body, err)
	}
	for _, kind := range []string{"message/rfc822", "text/plain"} {
		st := &state{remainingBodyBytes: 2}
		body := raw
		if kind == "message/rfc822" {
			body = message
		}
		n, err := parseNode(textproto.MIMEHeader{"Content-Type": {kind}, "Content-Transfer-Encoding": {"base64"}}, []byte(base64.StdEncoding.EncodeToString(body)), 0, 0, st)
		if err == nil {
			err = decodeTextLeaves(n, st)
		}
		if !errors.Is(err, ErrParseBudget) {
			t.Fatalf("%s lost fixed budget error: %v", kind, err)
		}
	}
}

func TestMultipartBudgetIncludesIgnoredBodies(t *testing.T) {
	for _, disposition := range []string{"inline", "attachment; filename=small.bin"} {
		body := []byte("--b\r\nContent-Type: application/octet-stream\r\nContent-Disposition: " + disposition + "\r\n\r\nabc\r\n--b--\r\n")
		header := textproto.MIMEHeader{"Content-Type": {"multipart/mixed; boundary=b"}}
		if _, err := parseNode(header, body, 0, 0, &state{remainingBodyBytes: 2}); !errors.Is(err, ErrParseBudget) {
			t.Fatalf("ignored body bypassed budget: %v", err)
		}
		if _, err := parseNode(header, body, 0, MaxMIMEDepth, &state{}); !errors.Is(err, ErrNestingLimit) {
			t.Fatalf("child read preceded depth gate: %v", err)
		}
		if _, err := parseNode(header, body, 0, 0, &state{parts: MaxMIMEParts - 1}); !errors.Is(err, ErrPartLimit) {
			t.Fatalf("child read preceded part gate: %v", err)
		}
	}
}

func TestUnselectedAlternativeConsumesTransferBudget(t *testing.T) {
	body := []byte("--b\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\naGV5\r\n--b\r\nContent-Type: text/html\r\nContent-Transfer-Encoding: base64\r\n\r\neWVz\r\n--b--\r\n")
	header := textproto.MIMEHeader{"Content-Type": {"multipart/alternative; boundary=b"}}
	// Eight encoded body bytes plus six decoded bytes, including the discarded
	// plain alternative. Tiny counters exercise the same production read guard.
	for _, budget := range []int{13, 14} {
		st := &state{remainingBodyBytes: budget}
		root, err := parseNode(header, body, 0, 0, st)
		if err != nil {
			t.Fatal(err)
		}
		err = decodeTextLeaves(root, st)
		if budget == 13 {
			if !errors.Is(err, ErrParseBudget) {
				t.Fatal("discarded alternative escaped shared budget", err)
			}
		} else if err != nil || st.remainingBodyBytes != 0 || !reflect.DeepEqual(selectParts(root), []BodyPart{{ContentType: "text/html", Text: "yes"}}) {
			t.Fatalf("ordinary selection/exact budget: %v %+v remaining=%d", err, selectParts(root), st.remainingBodyBytes)
		}
	}
}

// These are tiny structural-limit fixtures, not memory/exhaustion workloads.
func wrapSmallMultipart(message []byte, ordinal int) []byte {
	boundary := fmt.Sprintf("small-%d", ordinal)
	return []byte(fmt.Sprintf("Content-Type: multipart/mixed; boundary=%s\r\n\r\n--%s\r\n%s\r\n--%s--\r\n", boundary, boundary, message, boundary))
}

func TestStructuralDepthIncludesBothContainerKinds(t *testing.T) {
	for _, forwarded := range []bool{false, true} {
		message := []byte("Content-Type: text/plain\r\n\r\nordinary text")
		depth := 0
		if forwarded {
			message = append([]byte("Content-Type: message/rfc822\r\n\r\n"), message...)
			depth++
		}
		for ; depth < MaxMIMEDepth; depth++ {
			message = wrapSmallMultipart(message, depth)
		}
		want := []BodyPart{{ContentType: "text/plain", Text: "ordinary text"}}
		for i := 0; i < 2; i++ {
			got, err := Decode(message)
			if err != nil || !reflect.DeepEqual(got.Selected, want) {
				t.Fatalf("exact depth/independent decode: %+v %v", got.Selected, err)
			}
		}
		got, err := Decode(wrapSmallMultipart(message, depth))
		if !errors.Is(err, ErrNestingLimit) || got.Header != nil || len(got.Selected) != 0 {
			t.Fatalf("over-depth result not refused atomically: %+v %v", got, err)
		}
		if len(message) > 8192 || bytes.Count(message, []byte("ordinary text")) != 1 {
			t.Fatal("fixture must remain tiny")
		}
	}
}
