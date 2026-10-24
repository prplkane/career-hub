package materialwrite

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"

	"careerhub.local/backend/internal/materials/materialintegrity"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const maxExportReceipt = 512

var exportID = regexp.MustCompile(`^material_[0-9a-f]{64}$`)
var exportHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

type exportReceipt struct {
	SchemaVersion  int    `json:"schema_version"`
	ArtifactID     string `json:"artifact_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

func receiptBytes(id string, manifest []byte) []byte {
	raw, _ := json.Marshal(exportReceipt{1, id, exportDigest(manifest)})
	return append(raw, '\n')
}

func decodeReceipt(raw []byte, id string) (*exportReceipt, *Error) {
	var r exportReceipt
	if len(raw) == 0 || len(raw) > maxExportReceipt || json.Unmarshal(raw, &r) != nil ||
		r.SchemaVersion != 1 || !exportID.MatchString(r.ArtifactID) || r.ArtifactID != id || !exportHash.MatchString(r.ManifestSHA256) {
		return nil, failed("receipt", "MATERIAL_EXPORT_RECEIPT_INVALID")
	}
	canonical, _ := json.Marshal(r)
	if !bytes.Equal(append(canonical, '\n'), raw) {
		return nil, failed("receipt", "MATERIAL_EXPORT_RECEIPT_INVALID")
	}
	return &r, nil
}

type exportLocation struct{ artifact, manifest, receipt string }

func exportPaths(job, id, filename string) exportLocation {
	base := "outputs/application_materials/" + job
	version := base + "/exports/v1/" + id + "/"
	return exportLocation{version + filename, version + "manifest.json", base + "/export_receipts/" + id + ".json"}
}

type exportBundle struct {
	manifest                             *materialintegrity.Manifest
	content, manifestBytes, receiptBytes []byte
	paths                                exportLocation
	complete                             bool
}

func readExportFile(data *reads, path string, limit int64) ([]byte, bool, *Error) {
	b, ok, e := data.ReadFile(path, limit)
	if e != nil {
		return nil, false, failed("storage", e.Code)
	}
	return b, ok, nil
}

// The immutable manifest is the prepared record, not a completion signal. The
// separate receipt is written LAST. Recovery is monotonic and exact: never
// overwrite a conflict, repair a damaged completed export, delete history, or
// adopt a file not matching a freshly rendered, source-current identity.
func prepareExport(data *reads, expected *materialintegrity.Manifest, content []byte) (*exportBundle, *Error) {
	mraw, ie := materialintegrity.Encode(expected)
	if ie != nil {
		return nil, failed("integrity", ie.Code)
	}
	p := exportPaths(expected.Identity.Sources.JobID, expected.ArtifactID, expected.Identity.Filename)
	oldManifest, hasManifest, e := readExportFile(data, p.manifest, materialintegrity.MaxManifestBytes)
	if e != nil {
		return nil, e
	}
	oldContent, hasContent, e := readExportFile(data, p.artifact, materialintegrity.MaxArtifactBytes)
	if e != nil {
		return nil, e
	}
	oldReceipt, complete, e := readExportFile(data, p.receipt, maxExportReceipt)
	if e != nil {
		return nil, e
	}
	if complete && (!hasManifest || !hasContent) {
		return nil, failed("receipt", "MATERIAL_EXPORT_INCOMPLETE")
	}
	if !hasManifest && hasContent {
		return nil, failed("publication", "MATERIAL_EXPORT_PREPARATION_CONFLICT")
	}
	manifest := expected
	if hasManifest {
		manifest, ie = materialintegrity.Decode(oldManifest)
		if ie != nil {
			return nil, failed("integrity", ie.Code)
		}
		if manifest.ArtifactID != expected.ArtifactID {
			return nil, failed("publication", "MATERIAL_EXPORT_PREPARATION_CONFLICT")
		}
		// Creation time is not in the artifact identity. Keep the first manifest's
		// exact bytes/date on retry, including recovery after a process restart.
		mraw = oldManifest
	}
	if hasContent && !bytes.Equal(oldContent, content) {
		return nil, failed("integrity", "MATERIAL_ARTIFACT_CHANGED")
	}
	rraw := receiptBytes(manifest.ArtifactID, mraw)
	if complete {
		receipt, e := decodeReceipt(oldReceipt, manifest.ArtifactID)
		if e != nil {
			return nil, e
		}
		if _, ie := materialintegrity.Verify(mraw, receipt.ManifestSHA256, expected.Identity.Sources, oldContent); ie != nil {
			return nil, failed("integrity", ie.Code)
		}
		rraw = oldReceipt
	}
	// Charge future observations before any write, not after exceeding the
	// operation's aggregate input/readback envelope partway through publication.
	additional := 0
	for _, f := range []struct {
		path string
		raw  []byte
	}{{p.manifest, mraw}, {p.artifact, content}, {p.receipt, rraw}} {
		if !data.files[f.path].present {
			additional += len(f.raw)
		}
	}
	if additional > (32<<20)-data.bytes {
		return nil, failed("storage", "FILE_SIZE_LIMIT")
	}
	return &exportBundle{manifest, content, mraw, rraw, p, complete}, nil
}

func (b *exportBundle) publish(root *runtimepaths.Root, data *reads, out *Outcome, guard func() *Error) *Error {
	if b.complete {
		return nil
	} // common owner still verifies final observations
	out.Partial = data.files[b.paths.manifest].present
	files := []struct {
		path  string
		raw   []byte
		stage string
	}{
		{b.paths.manifest, b.manifestBytes, "export_manifest"},
		{b.paths.artifact, b.content, "export_artifact"},
		{b.paths.receipt, b.receiptBytes, "export_receipt"},
	}
	for _, f := range files {
		before, observed := data.files[f.path]
		if !observed {
			return failed("publication", "MATERIAL_PROPOSAL_INVALID")
		}
		if before.present {
			continue
		}
		if e := guard(); e != nil {
			return e
		}
		if _, e := root.EnsureDirectory(filepath.ToSlash(filepath.Dir(f.path))); e != nil {
			return failed("directory", e.Code)
		}
		if data.ctx.Err() != nil {
			return canceled()
		}
		published, e := data.s.create(root, f.path, f.raw)
		if published {
			if f.stage == "export_receipt" {
				out.Committed, out.Partial = true, false
			} else {
				out.Partial = true
			}
			data.files[f.path] = observation{bytes.Clone(f.raw), true, before.limit}
			data.bytes += len(f.raw)
		}
		if e != nil {
			out.CleanupFailed = e.Code == "FILE_CLOSE" || e.Code == "FILE_CLEANUP"
			return failed("publication", e.Code)
		}
		if !published {
			return failed("publication", "MATERIAL_SAVE_UNCONFIRMED")
		}
		if e := data.s.checkpoint(f.stage); e != nil {
			return e
		}
	}
	// Checkpoints/native failures may report an error after publication. The
	// common owner still performs readback/cleanup before exposing any content.
	return data.s.checkpoint("after_publish")
}

func readExport(data *reads, r ExportReadRequest, current materialintegrity.Sources) (*exportBundle, *Error) {
	p := exportPaths(r.JobID, r.ArtifactID, "")
	rraw, present, e := readExportFile(data, p.receipt, maxExportReceipt)
	if e != nil {
		return nil, e
	}
	if !present {
		return nil, failed("receipt", "MATERIAL_EXPORT_RECEIPT_REQUIRED")
	}
	receipt, e := decodeReceipt(rraw, r.ArtifactID)
	if e != nil {
		return nil, e
	}
	mraw, present, e := readExportFile(data, p.manifest, materialintegrity.MaxManifestBytes)
	if e != nil {
		return nil, e
	}
	if !present {
		return nil, failed("receipt", "MATERIAL_EXPORT_INCOMPLETE")
	}
	m, ie := materialintegrity.Decode(mraw)
	if ie != nil {
		return nil, failed("integrity", ie.Code)
	}
	if m.ArtifactID != r.ArtifactID || m.Identity.Sources.JobID != r.JobID || m.Identity.Sources.Kind != r.Kind {
		return nil, failed("integrity", "MATERIAL_EXPORT_IDENTITY_MISMATCH")
	}
	p = exportPaths(r.JobID, r.ArtifactID, m.Identity.Filename)
	content, present, e := readExportFile(data, p.artifact, materialintegrity.MaxArtifactBytes)
	if e != nil {
		return nil, e
	}
	if !present {
		return nil, failed("receipt", "MATERIAL_EXPORT_INCOMPLETE")
	}
	if _, ie = materialintegrity.Verify(mraw, receipt.ManifestSHA256, current, content); ie != nil {
		return nil, failed("integrity", ie.Code)
	}
	return &exportBundle{m, content, mraw, rraw, p, true}, nil
}
