package readonly

import (
	"careerhub.local/backend/internal/storage/runtimepaths"
	"math"
	"testing"
)

func TestIntakeSourcesPage(t *testing.T) {
	entries := []runtimepaths.FileMetadata{{Name: "z.TXT", Size: 9}, {Name: "B.docx", Size: 4}, {Name: "a.txt", Size: 2}, {Name: "ignored.pdf", Size: 8}}
	page := intakeSourcePage(entries, 0, 2)
	if page.Total != 3 || len(page.Items) != 2 || page.Items[0].Name != "a.txt" || page.Items[1].Path != "career_vault/raw/B.docx" || page.Items[1].SizeBytes != 4 || page.NextCursor == nil || *page.NextCursor != "offset:2" {
		t.Fatal(page)
	}
	page = intakeSourcePage(entries, 2, 2)
	if len(page.Items) != 1 || page.Items[0].Name != "z.TXT" || page.NextCursor != nil {
		t.Fatal(page)
	}
	page = intakeSourcePage(entries, math.MaxInt64, 100)
	if page.Items == nil || len(page.Items) != 0 || page.Total != 3 || page.NextCursor != nil {
		t.Fatal(page)
	}
	if entries[0].Name != "z.TXT" {
		t.Fatal("projection mutated caller input")
	}
}
