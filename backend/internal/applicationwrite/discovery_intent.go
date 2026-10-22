package applicationwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
)

// Must fit native Read/Create/RemoveFile, not only the JSON decoder. Capture
// has one replacement; deletion preimages total <=8 MiB before base64 encoding.
const discoveryIntentLimit = 16 << 20

type discoveryFileIntent struct {
	Change discovery.SourceChange `json:"change"`
	Before discovery.RowWitness   `json:"before_row"`
	After  discovery.RowWitness   `json:"after_row"`
}
type discoveryReadGuard struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Size    int    `json:"size"`
	SHA256  string `json:"sha256"`
}
type discoveryIntent struct {
	Version   int                   `json:"version"`
	Home      datahome.Info         `json:"home"`
	Operation string                `json:"operation"`
	Files     []discoveryFileIntent `json:"files"`
	Guards    []discoveryReadGuard  `json:"guards"`
}
type discoveryEnvelope struct {
	Payload json.RawMessage `json:"payload"`
	SHA256  string          `json:"sha256"`
}
type discoveryPublication struct {
	intent            discoveryIntent
	raw               []byte
	recorded, removed bool
	applied           int
	cleanupCode       string
	state             string
}

func digestBytes(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func sourcePathID(path string) (string, bool) {
	const prefix = "jobs/discovery/captures/"
	const suffix = "/source.txt"
	if !utf8.ValidString(path) || !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	checked, e := discovery.NormalizeID(id)
	return id, e == nil && checked == id && id != ".." && !strings.ContainsRune(id, ':')
}
func validWitness(w discovery.RowWitness, id string) bool {
	return w.DiscoveryID == id && ((w.Present && validDigest(w.SHA256)) || (!w.Present && w.SHA256 == ""))
}
func validDiscoveryIntent(p discoveryIntent) bool {
	if p.Version != 1 || p.Home.FormatVersion != 2 || !p.Home.Initialized || len(p.Home.DataHomeID) != 35 || !strings.HasPrefix(p.Home.DataHomeID, "dh_") {
		return false
	}
	if p.Home.Channel != "dev" && p.Home.Channel != "stable" {
		return false
	}
	for _, c := range p.Home.DataHomeID[3:] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	if len(p.Files) < 1 || len(p.Files) > 10000 || len(p.Guards) > 10000 {
		return false
	}
	if p.Operation != "capture" && p.Operation != "delete" || p.Operation == "capture" && len(p.Files) != 1 {
		return false
	}
	seen := map[string]bool{}
	beforeTotal, afterTotal := 0, 0
	for _, f := range p.Files {
		c := f.Change
		id, ok := sourcePathID(c.RelativePath)
		folded := textcompat.Fold(c.RelativePath)
		if !ok || seen[folded] || !validWitness(f.Before, id) || !validWitness(f.After, id) || f.Before == f.After {
			return false
		}
		seen[folded] = true
		if !c.BeforePresent && len(c.Before) != 0 || !c.AfterPresent && len(c.After) != 0 || !c.BeforePresent && !c.AfterPresent {
			return false
		}
		if c.BeforePresent && !f.Before.Present || f.After.Present != c.AfterPresent {
			return false
		}
		if (p.Operation == "capture") != c.AfterPresent {
			return false
		}
		if len(c.Before) > 4<<20 || len(c.After) > 4<<20 {
			return false
		}
		beforeTotal += len(c.Before)
		afterTotal += len(c.After)
		if beforeTotal > 8<<20 || afterTotal > 8<<20 {
			return false
		}
		if c.AfterPresent && (len(c.After) == 0 || !utf8.Valid(c.After) || bytes.ContainsAny(c.After, "\r\x00") || utf8.RuneCount(c.After) > 500000) {
			return false
		}
	}
	for _, g := range p.Guards {
		_, ok := sourcePathID(g.Path)
		folded := textcompat.Fold(g.Path)
		if !ok || seen[folded] || g.Size < 0 || g.Size > 4<<20 {
			return false
		}
		seen[folded] = true
		if g.Present && !validDigest(g.SHA256) || !g.Present && (g.Size != 0 || g.SHA256 != "") {
			return false
		}
	}
	return true
}
func canonicalIntentJSON(raw []byte, target any) bool {
	if len(raw) == 0 || len(raw) > discoveryIntentLimit || !utf8.Valid(raw) {
		return false
	}
	depth, nodes, quoted, escaped := 0, 0, false, false
	for _, b := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			nodes++
		case '}', ']':
			depth--
		case ',', ':':
			nodes++
		}
		if depth < 0 || depth > 16 || nodes > 500000 {
			return false
		}
	}
	// Strict canonical re-encoding rejects duplicates, unknown fields, altered
	// key spelling, invalid scalar escapes and trailing content. Base64 is bounded.
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return false
	}
	encoded, e := json.Marshal(target)
	return e == nil && bytes.Equal(encoded, raw)
}
func encodeDiscoveryIntent(p discoveryIntent) ([]byte, *Error) {
	if !validDiscoveryIntent(p) {
		return nil, failed("intent", "DISCOVERY_INTENT_INVALID")
	}
	payload, e := json.Marshal(p)
	if e != nil {
		return nil, failed("intent", "DISCOVERY_INTENT_INVALID")
	}
	raw, e := json.Marshal(discoveryEnvelope{Payload: payload, SHA256: digestBytes(payload)})
	if e != nil || len(raw) > discoveryIntentLimit {
		return nil, failed("intent", "DISCOVERY_INTENT_LIMIT")
	}
	return raw, nil
}
func decodeDiscoveryIntent(raw []byte, home datahome.Info) (discoveryIntent, *Error) {
	var outer discoveryEnvelope
	var p discoveryIntent
	if !canonicalIntentJSON(raw, &outer) || outer.SHA256 != digestBytes(outer.Payload) || !canonicalIntentJSON(outer.Payload, &p) || !validDiscoveryIntent(p) {
		return p, failed("intent", "DISCOVERY_INTENT_INVALID")
	}
	if p.Home != home {
		return p, failed("intent", "DISCOVERY_INTENT_HOME_CHANGED")
	}
	return p, nil
}

func (r *sourceReads) planPublication(ctx context.Context, tx *sql.Tx, changes []discovery.SourceChange, before []discovery.RowWitness) *Error {
	if r.pendingPublication() != nil || len(r.files) != 0 {
		return failed("intent", "DISCOVERY_INTENT_INVALID")
	}
	prior := map[string]discovery.RowWitness{}
	for _, w := range before {
		prior[w.DiscoveryID] = w
	}
	ids := make([]string, len(changes))
	paths := map[string]bool{}
	for i, c := range changes {
		var ok bool
		ids[i], ok = sourcePathID(c.RelativePath)
		if !ok {
			return failed("intent", "DISCOVERY_INTENT_INVALID")
		}
		paths[c.RelativePath] = true
	}
	after, de := discovery.MutationWitnesses(ctx, tx, ids)
	if de != nil {
		return discoveryProblem(de)
	}
	p := discoveryIntent{Version: 1, Home: r.home, Operation: "delete", Files: make([]discoveryFileIntent, len(changes)), Guards: []discoveryReadGuard{}}
	for i, c := range changes {
		w, ok := prior[ids[i]]
		if !ok {
			return failed("intent", "DISCOVERY_WITNESS_MISSING")
		}
		p.Files[i] = discoveryFileIntent{c, w, after[i]}
		if c.AfterPresent {
			p.Operation = "capture"
		}
	}
	for _, seen := range r.captures {
		if paths[seen.path] {
			continue
		}
		g := discoveryReadGuard{Path: seen.path, Present: seen.present, Size: len(seen.raw)}
		if seen.present {
			g.SHA256 = digestBytes(seen.raw)
		}
		p.Guards = append(p.Guards, g)
	}
	raw, e := encodeDiscoveryIntent(p)
	if e != nil {
		return e
	}
	// Freeze private file preimages, witnesses and guards. The future publication
	// cannot borrow mutable request-owned byte slices.
	frozen, e := decodeDiscoveryIntent(raw, r.home)
	if e != nil {
		return e
	}
	r.publication = &discoveryPublication{intent: frozen, raw: raw}
	return nil
}
func (p *discoveryPublication) changes() []discovery.SourceChange {
	out := make([]discovery.SourceChange, len(p.intent.Files))
	for i, f := range p.intent.Files {
		out[i] = f.Change
	}
	return out
}
func (p *discoveryPublication) databaseSide(ctx context.Context, tx *sql.Tx) (string, *Error) {
	ids := make([]string, len(p.intent.Files))
	for i, f := range p.intent.Files {
		ids[i] = f.Before.DiscoveryID
	}
	current, de := discovery.MutationWitnesses(ctx, tx, ids)
	if de != nil {
		return "", discoveryProblem(de)
	}
	before, after := true, true
	for i, w := range current {
		before = before && w == p.intent.Files[i].Before
		after = after && w == p.intent.Files[i].After
	}
	if after && !before {
		return "committed", nil
	}
	if before && !after {
		return "rolled_back", nil
	}
	return "", failed("witness", "DISCOVERY_DATABASE_STATE_CHANGED")
}
func (p *discoveryPublication) checkJournal(root *runtimepaths.Root) *Error {
	raw, present, e := root.ReadSingleLinkFile(datahome.DiscoveryRecoveryPath, discoveryIntentLimit)
	if e != nil || !present || !bytes.Equal(raw, p.raw) {
		return failed("intent", "DISCOVERY_INTENT_CHANGED")
	}
	return nil
}
func (p *discoveryPublication) checkHome(root *runtimepaths.Root, home datahome.Info) *Error {
	current, e := datahome.InspectOwnedForDiscoveryRecovery(root, nil)
	if e != nil {
		return failed("admission", e.Code)
	}
	if *current != home || home != p.intent.Home {
		return failed("admission", "DISCOVERY_INTENT_HOME_CHANGED")
	}
	return nil
}
func (p *discoveryPublication) checkGuards(ctx context.Context, root *runtimepaths.Root) *Error {
	for _, g := range p.intent.Guards {
		if ctx.Err() != nil {
			return canceled()
		}
		raw, present, e := root.ReadSingleLinkFile(g.Path, 4<<20)
		if e != nil || present != g.Present || len(raw) != g.Size || present && digestBytes(raw) != g.SHA256 {
			return failed("source", "DISCOVERY_SOURCE_CHANGED")
		}
	}
	return nil
}
func observeSource(root *runtimepaths.Root, c discovery.SourceChange) (before, after bool, problem *Error) {
	raw, present, e := root.ReadSingleLinkFile(c.RelativePath, 4<<20)
	if e != nil {
		return false, false, failed("source", "DISCOVERY_SOURCE_UNAVAILABLE")
	}
	return present == c.BeforePresent && bytes.Equal(raw, c.Before), present == c.AfterPresent && bytes.Equal(raw, c.After), nil
}
func (p *discoveryPublication) checkFiles(ctx context.Context, root *runtimepaths.Root, side string) *Error {
	for _, f := range p.intent.Files {
		if ctx.Err() != nil {
			return canceled()
		}
		before, after, e := observeSource(root, f.Change)
		if e != nil {
			return e
		}
		if side == "before" && !before || side == "after" && !after || side == "either" && !before && !after {
			return failed("source", "DISCOVERY_SOURCE_CHANGED")
		}
	}
	return p.checkGuards(ctx, root)
}
func (p *discoveryPublication) record(ctx context.Context, root *runtimepaths.Root, tx *sql.Tx, home datahome.Info) *Error {
	if ctx.Err() != nil {
		return canceled()
	}
	if e := p.checkHome(root, home); e != nil {
		return e
	}
	_, present, pe := root.ReadSingleLinkFile(datahome.DiscoveryRecoveryPath, discoveryIntentLimit)
	if pe != nil {
		return failed("intent", "DISCOVERY_INTENT_UNAVAILABLE")
	}
	if present {
		return failed("intent", "DATA_HOME_RECOVERY_REQUIRED")
	}
	if e := p.checkFiles(ctx, root, "before"); e != nil {
		return e
	}
	side, e := p.databaseSide(ctx, tx)
	if e != nil {
		return e
	}
	if side != "committed" {
		return failed("witness", "DISCOVERY_DATABASE_STATE_CHANGED")
	}
	if de := discovery.CheckPublicationReferences(ctx, tx, p.changes()); de != nil {
		return discoveryProblem(de)
	}
	if ctx.Err() != nil {
		return canceled()
	}
	p.recorded, pe = root.CreateFile(datahome.DiscoveryRecoveryPath, p.raw)
	if pe != nil || !p.recorded {
		return failed("intent", "DISCOVERY_INTENT_WRITE_FAILED")
	}
	return nil
}
func (p *discoveryPublication) checkReady(ctx context.Context, root *runtimepaths.Root, tx *sql.Tx, pins *runtimepaths.SQLiteFiles, home datahome.Info) *Error {
	if ctx.Err() != nil {
		return canceled()
	}
	if e := p.checkHome(root, home); e != nil {
		return e
	}
	if e := p.checkJournal(root); e != nil {
		return e
	}
	if e := p.checkFiles(ctx, root, "before"); e != nil {
		return e
	}
	if pins == nil || pins.Check() != nil {
		return failed("path", "DISCOVERY_DATABASE_PATH_CHANGED")
	}
	if e := schemaCurrent(ctx, tx); e != nil {
		return e
	}
	side, e := p.databaseSide(ctx, tx)
	if e != nil {
		return e
	}
	if side != "committed" {
		return failed("witness", "DISCOVERY_DATABASE_STATE_CHANGED")
	}
	return nil
}

func (p *discoveryPublication) finishCommitted(ctx context.Context, root *runtimepaths.Root, conn *sql.Conn, pins *runtimepaths.SQLiteFiles, home datahome.Info, checkpoint func(string) *Error, rollback func(*sql.Tx) error) (problem *Error) {
	tx, e := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if e != nil {
		return failed("verify", "DISCOVERY_VERIFY_TRANSACTION_FAILED")
	}
	defer func() {
		if e := rollback(tx); e != nil {
			p.cleanupCode = "DISCOVERY_VERIFY_ROLLBACK_FAILED"
			if problem == nil {
				problem = failed("cleanup", p.cleanupCode)
			}
		}
	}()
	return p.settle(ctx, root, tx, pins, home, "committed", checkpoint)
}

// settle holds an IMMEDIATE SQLite transaction throughout file publication.
// SQL committed first, therefore a rolled-back witness may ONLY keep verified
// preimages. Recovery never restores bytes over an unexpected external change.
// All files are classified before the first effect; a third state fails closed.
func (p *discoveryPublication) settle(ctx context.Context, root *runtimepaths.Root, tx *sql.Tx, pins *runtimepaths.SQLiteFiles, home datahome.Info, expected string, checkpoint func(string) *Error) *Error {
	boundary := func() *Error {
		if ctx.Err() != nil {
			return canceled()
		}
		if e := p.checkHome(root, home); e != nil {
			return e
		}
		if e := p.checkJournal(root); e != nil {
			return e
		}
		if pins == nil || pins.Check() != nil {
			return failed("path", "DISCOVERY_DATABASE_PATH_CHANGED")
		}
		return p.checkGuards(ctx, root)
	}
	if e := boundary(); e != nil {
		return e
	}
	if e := schemaCurrent(ctx, tx); e != nil {
		return e
	}
	side, e := p.databaseSide(ctx, tx)
	if e != nil {
		return e
	}
	if expected != "" && side != expected {
		return failed("witness", "DISCOVERY_DATABASE_STATE_CHANGED")
	}
	p.state = side
	fileSide := "either"
	if side == "rolled_back" {
		fileSide = "before"
	}
	if e := p.checkFiles(ctx, root, fileSide); e != nil {
		return e
	}
	if de := discovery.CheckPublicationReferences(ctx, tx, p.changes()); de != nil {
		return discoveryProblem(de)
	}
	if side == "committed" {
		if de := discovery.CheckPublicationContent(ctx, tx, p.changes()); de != nil {
			return discoveryProblem(de)
		}
		for i, f := range p.intent.Files {
			if e := boundary(); e != nil {
				return e
			}
			before, after, e := observeSource(root, f.Change)
			if e != nil {
				return e
			}
			if !after {
				if !before {
					return failed("source", "DISCOVERY_SOURCE_CHANGED")
				}
				c := f.Change
				var changed bool
				var pe *runtimepaths.Error
				switch {
				case !c.AfterPresent:
					changed, pe = root.RemoveFile(c.RelativePath, c.Before)
				case c.BeforePresent:
					changed, pe = root.ReplaceFile(c.RelativePath, c.Before, c.After)
				default:
					_, pe = root.EnsureDirectory(strings.TrimSuffix(c.RelativePath, "/source.txt"))
					if pe == nil {
						changed, pe = root.CreateFile(c.RelativePath, c.After)
					}
				}
				if changed {
					p.applied++
				}
				if pe != nil || !changed {
					return failed("publish", "DISCOVERY_SOURCE_PUBLICATION_FAILED")
				}
			}
			if e := checkpoint("discovery_file:" + strconv.Itoa(i)); e != nil {
				return e
			}
		}
		fileSide = "after"
	}
	if e := checkpoint("discovery_files_complete"); e != nil {
		return e
	}
	if e := boundary(); e != nil {
		return e
	}
	if e := p.checkFiles(ctx, root, fileSide); e != nil {
		return e
	}
	observed, e := p.databaseSide(ctx, tx)
	if e != nil {
		return e
	}
	if observed != side {
		return failed("witness", "DISCOVERY_DATABASE_STATE_CHANGED")
	}
	if de := discovery.CheckPublicationReferences(ctx, tx, p.changes()); de != nil {
		return discoveryProblem(de)
	}
	if side == "committed" {
		if de := discovery.CheckPublicationContent(ctx, tx, p.changes()); de != nil {
			return discoveryProblem(de)
		}
	}
	// The complete settlement is verified before removing this exact intent.
	// Preserve a true removed flag even if the native helper reports a late error.
	var pe *runtimepaths.Error
	p.removed, pe = root.RemoveFile(datahome.DiscoveryRecoveryPath, p.raw)
	if pe != nil || !p.removed {
		return failed("cleanup", "DISCOVERY_INTENT_REMOVE_FAILED")
	}
	return checkpoint("discovery_journal_removed")
}
