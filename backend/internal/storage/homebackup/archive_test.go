package homebackup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"strings"
	"testing"
)

const testHomeID = "dh_0123456789abcdef0123456789abcdef"

func testManifest(files ...File) Manifest {
	if files == nil {
		files = []File{}
	}
	return Manifest{FormatVersion, 2, testHomeID, "stable", nil, files}
}

func testFile(name, content string) File {
	h := sha256.Sum256([]byte(content))
	return File{name, int64(len(content)), hex.EncodeToString(h[:])}
}

func manifestJSON(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type fixtureEntry struct {
	name, content string
	mode          fs.FileMode
}

func fixtureZIP(t *testing.T, raw []byte, entries ...fixtureEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	if raw != nil {
		entries = append([]fixtureEntry{{ManifestName, string(raw), 0600}}, entries...)
	}
	for _, entry := range entries {
		h := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		h.SetMode(entry.mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(f, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func inspectBytes(raw []byte) (*Archive, *Error) {
	return Inspect(context.Background(), bytes.NewReader(raw), int64(len(raw)))
}

func TestArchiveRoundTripAndDefensiveManifest(t *testing.T) {
	file := testFile("jobs/данные-😀.txt", "synthetic job\n")
	m := testManifest(file)
	schema := int64(0)
	m.SQLiteSchemaVersion = &schema
	raw := fixtureZIP(t, manifestJSON(t, m), fixtureEntry{file.RelativePath, "synthetic job\n", 0600})
	ctx, cancel := context.WithCancel(context.Background())
	a, problem := Inspect(ctx, bytes.NewReader(raw), int64(len(raw)))
	if problem != nil {
		t.Fatal(problem)
	}
	cancel() // Verify must not inherit the completed inspection's deadline.
	copy := a.Manifest()
	copy.Files[0].SHA256 = strings.Repeat("0", 64)
	*copy.SQLiteSchemaVersion = 999
	if a.Manifest().SQLiteSchemaVersion == copy.SQLiteSchemaVersion || a.Manifest().Files[0] != file {
		t.Fatal("manifest was aliased")
	}
	if problem = a.Verify(context.Background()); problem != nil {
		t.Fatal(problem)
	}
}

func TestEmptyArchiveAndInvalidReceiver(t *testing.T) {
	a, problem := inspectBytes(fixtureZIP(t, manifestJSON(t, testManifest())))
	if problem != nil {
		t.Fatal(problem)
	}
	if problem = a.Verify(context.Background()); problem != nil {
		t.Fatal(problem)
	}
	if (&Archive{}).Verify(context.Background()) == nil {
		t.Fatal("zero Archive accepted")
	}
	if (*Archive)(nil).Verify(context.Background()) == nil {
		t.Fatal("nil Archive accepted")
	}
}

func TestInspectRefusesInvalidArchives(t *testing.T) {
	f := testFile("jobs/a.txt", "data")
	good := manifestJSON(t, testManifest(f))
	for _, tc := range []struct {
		name     string
		manifest []byte
		entries  []fixtureEntry
	}{
		{"missing-manifest", nil, nil},
		{"missing-payload", good, nil},
		{"extra-payload", manifestJSON(t, testManifest()), []fixtureEntry{{"jobs/a.txt", "data", 0600}}},
		{"size-mismatch", good, []fixtureEntry{{"jobs/a.txt", "different", 0600}}},
		{"duplicate", good, []fixtureEntry{{"jobs/a.txt", "data", 0600}, {"jobs/a.txt", "data", 0600}}},
		{"casefold", good, []fixtureEntry{{"jobs/Straße", "", 0600}, {"jobs/STRASSE", "", 0600}}},
		{"manifest-casefold", good, []fixtureEntry{{"MANIFEST.JSON", "", 0600}}},
		{"traversal", good, []fixtureEntry{{"jobs/../a.txt", "", 0600}}},
		{"absolute", good, []fixtureEntry{{"/jobs/a.txt", "", 0600}}},
		{"drive", good, []fixtureEntry{{"C:/jobs/a.txt", "", 0600}}},
		{"backslash", good, []fixtureEntry{{`jobs\a.txt`, "", 0600}}},
		{"directory", good, []fixtureEntry{{"jobs/", "", fs.ModeDir | 0700}}},
		{"symlink", good, []fixtureEntry{{"jobs/a.txt", "data", fs.ModeSymlink | 0600}}},
		{"device", good, []fixtureEntry{{"jobs/a.txt", "data", fs.ModeDevice | 0600}}},
		{"invalid-manifest", []byte(`{"files":[]}`), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, problem := inspectBytes(fixtureZIP(t, tc.manifest, tc.entries...)); problem == nil {
				t.Fatal("accepted invalid archive")
			}
		})
	}
	if _, p := inspectBytes([]byte("not a zip")); p == nil {
		t.Fatal("accepted non ZIP")
	}
}

func TestPayloadVerificationIsSeparateAndChecksCRC(t *testing.T) {
	for _, crc := range []bool{false, true} {
		file := testFile("jobs/a.txt", "payload-A")
		content := "payload-B"
		if crc {
			content = "payload-A"
		}
		raw := fixtureZIP(t, manifestJSON(t, testManifest(file)), fixtureEntry{file.RelativePath, content, 0600})
		if crc {
			pos := bytes.Index(raw, []byte("payload-A"))
			if pos < 0 {
				t.Fatal("fixture payload absent")
			}
			raw[pos] = 'x' // Preserve manifest and directory; invalidate payload CRC.
		}
		a, problem := inspectBytes(raw)
		if problem != nil {
			t.Fatal(problem)
		}
		if problem = a.Verify(context.Background()); problem == nil || problem.Code != "BACKUP_PAYLOAD_INVALID" {
			t.Fatalf("CRC=%v: %v", crc, problem)
		}
	}
}

type cancelReader struct {
	*bytes.Reader
	cancel context.CancelFunc
	armed  bool
}

func (r *cancelReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.Reader.ReadAt(p, off)
	if r.armed {
		r.cancel()
		r.armed = false
	}
	return n, err
}

func TestCancellationAndReadBudgets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, p := Inspect(ctx, bytes.NewReader(nil), 0); p == nil || p.Code != "BACKUP_CANCELED" {
		t.Fatal(p)
	}
	if _, p := Inspect(context.Background(), bytes.NewReader(nil), MaxArchiveBytes+1); p == nil || p.Code != "BACKUP_SIZE_LIMIT" {
		t.Fatal(p)
	}
	input := &archiveSource{source: bytes.NewReader([]byte("12345678")), ctx: context.Background(), metadata: true, remaining: 4}
	if _, err := input.ReadAt(make([]byte, 3), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := input.ReadAt(make([]byte, 2), 3); err == nil {
		t.Fatal("metadata budget exceeded")
	}
	file := testFile("jobs/a.txt", "data")
	raw := fixtureZIP(t, manifestJSON(t, testManifest(file)), fixtureEntry{file.RelativePath, "data", 0600})
	inspectCtx, inspectCancel := context.WithCancel(context.Background())
	defer inspectCancel()
	interrupt := &cancelReader{Reader: bytes.NewReader(raw), cancel: inspectCancel, armed: true}
	if _, p := Inspect(inspectCtx, interrupt, int64(len(raw))); p == nil || p.Code != "BACKUP_CANCELED" {
		t.Fatalf("inspection cancellation was misreported: %v", p)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	r := &cancelReader{Reader: bytes.NewReader(raw), cancel: cancel}
	a, p := Inspect(context.Background(), r, int64(len(raw)))
	if p != nil {
		t.Fatal(p)
	}
	r.armed = true
	if p = a.Verify(ctx); p == nil || p.Code != "BACKUP_CANCELED" {
		t.Fatal(p)
	}
}

func TestEncryptedAndUnsupportedCompression(t *testing.T) {
	file := testFile("jobs/a.txt", "data")
	for _, encrypted := range []bool{false, true} {
		raw := fixtureZIP(t, manifestJSON(t, testManifest(file)), fixtureEntry{file.RelativePath, "data", 0600})
		// The last central-directory header belongs to our one payload entry.
		central := bytes.LastIndex(raw, []byte{'P', 'K', 1, 2})
		if central < 0 {
			t.Fatal("central directory missing")
		}
		local := int(binary.LittleEndian.Uint32(raw[central+42 : central+46]))
		if encrypted {
			raw[central+8] |= 1
			raw[local+6] |= 1
		} else {
			binary.LittleEndian.PutUint16(raw[central+10:central+12], 99)
			binary.LittleEndian.PutUint16(raw[local+8:local+10], 99)
		}
		a, p := inspectBytes(raw)
		if encrypted {
			if p == nil || p.Code != "BACKUP_ENTRY_INVALID" {
				t.Fatal("encrypted archive accepted", p)
			}
		} else {
			if p != nil {
				t.Fatal(p)
			}
			if p = a.Verify(context.Background()); p == nil {
				t.Fatal("unsupported compression verified")
			}
		}
	}
}
