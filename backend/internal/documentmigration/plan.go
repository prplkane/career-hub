// Package documentmigration contains the bounded native document-migration
// domain contract. It has no filesystem, credential-store, DPAPI, or process
// authority; those boundaries are composed separately.
package documentmigration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	SourceFormatVersion = 1
	TargetFormatVersion = 2
	planContract        = "SEC-240-v1-to-v2"
)

// Plan is the content-minimized review record exposed by the source migration.
type Plan struct {
	PlanID               string
	SourceFormatVersion  int
	TargetFormatVersion  int
	DocumentCount        int
	CiphertextFileCount  int
	TotalCiphertextBytes int
}

// validatedDocument and validatedSnapshot are deliberately private. A future
// source reader may construct them only after marker, ledger, DPAPI, membership,
// size, and plaintext-integrity validation. This pure function grants none of
// that authority by itself.
type validatedDocument struct {
	ID         string
	Ciphertext validatedCiphertext
}

type validatedSnapshot struct {
	MarkerSHA256         string
	DocumentsRootPresent bool
	Ledger               *validatedCiphertext
	Documents            []validatedDocument
}

type validatedCiphertext struct {
	Size   int
	SHA256 string
}

type fingerprintFile struct {
	Kind     string `json:"kind"`
	ObjectID string `json:"object_id,omitempty"`
	SHA256   string `json:"sha256"`
	Size     int    `json:"size"`
}

// Field order is lexical to reproduce Python json.dumps(sort_keys=True).
type fingerprint struct {
	Contract             string            `json:"contract"`
	DocumentsRootPresent bool              `json:"documents_root_present"`
	Files                []fingerprintFile `json:"files"`
	MarkerSHA256         string            `json:"marker_sha256"`
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func observedCiphertext(content []byte) validatedCiphertext {
	return validatedCiphertext{Size: len(content), SHA256: digest(content)}
}

// planForValidatedSnapshot reproduces Python _plan_for_snapshot exactly for a
// caller-proven snapshot. List order remains ledger first, then ledger record
// order; JSON object keys are emitted in Python's sorted-key order.
func planForValidatedSnapshot(snapshot validatedSnapshot) Plan {
	files := make([]fingerprintFile, 0, len(snapshot.Documents)+1)
	total := 0
	if snapshot.Ledger != nil {
		files = append(files, fingerprintFile{
			Kind: "ledger", SHA256: snapshot.Ledger.SHA256,
			Size: snapshot.Ledger.Size,
		})
		total += snapshot.Ledger.Size
	}
	for _, document := range snapshot.Documents {
		files = append(files, fingerprintFile{
			Kind: "blob", ObjectID: document.ID,
			SHA256: document.Ciphertext.SHA256, Size: document.Ciphertext.Size,
		})
		total += document.Ciphertext.Size
	}
	payload, err := json.Marshal(fingerprint{
		Contract:             planContract,
		DocumentsRootPresent: snapshot.DocumentsRootPresent,
		Files:                files,
		MarkerSHA256:         snapshot.MarkerSHA256,
	})
	if err != nil {
		// The fingerprint contains only fixed strings, booleans, integers, and
		// lowercase ASCII digests. encoding/json cannot fail for this shape.
		panic("document migration fingerprint serialization failed")
	}
	return Plan{
		PlanID:               "sha256:" + digest(payload),
		SourceFormatVersion:  SourceFormatVersion,
		TargetFormatVersion:  TargetFormatVersion,
		DocumentCount:        len(snapshot.Documents),
		CiphertextFileCount:  len(files),
		TotalCiphertextBytes: total,
	}
}
