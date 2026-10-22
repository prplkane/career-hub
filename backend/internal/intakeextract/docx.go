package intakeextract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
const contentTypesNS = "http://schemas.openxmlformats.org/package/2006/content-types"
const packageRelationshipsNS = "http://schemas.openxmlformats.org/package/2006/relationships"
const officeRelationshipsNS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"

const documentContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
const stylesContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"

const (
	maxDOCXBytes     = 10 << 20
	maxDOCXExpanded  = 50 << 20
	maxDocumentXML   = 16 << 20 // Native first-pass ceiling, tighter than source.
	maxStylesXML     = 2 << 20
	maxOPCXML        = 512 << 10
	maxDocumentNodes = 250000
)

// ParseDOCXBlocks reads ordinary paragraphs and top-level table-cell
// paragraphs in source order. Unsupported/malformed packages fail closed with
// no partial blocks. It is a pure bounded adapter, not a filesystem reader.
func ParseDOCXBlocks(ctx context.Context, content []byte) ([]Block, error) {
	if ctx == nil || len(content) == 0 {
		return nil, ErrInput
	}
	if len(content) > maxDOCXBytes {
		return nil, ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, ErrInput
	}
	var document, styles, contentTypes, rootRelationships, documentRelationships *zip.File
	var expanded uint64
	seen := make(map[string]struct{}, len(archive.File))
	if len(archive.File) > 2048 {
		return nil, ErrLimit
	}
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := file.Name
		entry := strings.TrimSuffix(name, "/")
		if entry == "" || !utf8.ValidString(name) || strings.ContainsRune(name, 0) ||
			strings.HasPrefix(name, "/") || strings.Contains(name, "\\") ||
			path.Clean(entry) != entry || strings.HasPrefix(entry, "../") ||
			(strings.HasSuffix(name, "/") != file.FileInfo().IsDir()) ||
			(!file.FileInfo().IsDir() && !file.Mode().IsRegular()) {
			return nil, ErrInput
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, ErrInput
		}
		seen[name] = struct{}{}
		if file.UncompressedSize64 > maxDOCXExpanded-expanded {
			return nil, ErrLimit
		}
		expanded += file.UncompressedSize64
		switch name {
		case "[Content_Types].xml":
			contentTypes = file
		case "_rels/.rels":
			rootRelationships = file
		case "word/document.xml":
			document = file
		case "word/styles.xml":
			styles = file
		case "word/_rels/document.xml.rels":
			documentRelationships = file
		}
	}
	if contentTypes == nil || rootRelationships == nil || document == nil ||
		styles == nil || documentRelationships == nil {
		return nil, ErrInput
	}
	for _, check := range []struct {
		part     *zip.File
		required string
		target   string
		types    bool
	}{
		{contentTypes, "", "", true},
		{rootRelationships, officeRelationshipsNS + "officeDocument", "word/document.xml", false},
		{documentRelationships, officeRelationshipsNS + "styles", "styles.xml", false},
	} {
		raw, e := readPart(ctx, check.part, maxOPCXML)
		if e != nil {
			return nil, e
		}
		if check.types {
			e = validateContentTypes(ctx, raw)
		} else {
			e = validateRelationships(ctx, raw, check.required, check.target)
		}
		if e != nil {
			return nil, e
		}
	}
	styleRaw, err := readPart(ctx, styles, maxStylesXML)
	if err != nil {
		return nil, err
	}
	styleNames, err := readStyleNames(ctx, styleRaw)
	if err != nil {
		return nil, err
	}
	raw, err := readPart(ctx, document, maxDocumentXML)
	if err != nil {
		return nil, err
	}
	return readDocumentBlocks(ctx, raw, styleNames)
}

func opcAttribute(element xml.StartElement, local string) string {
	for _, item := range element.Attr {
		if item.Name.Space == "" && item.Name.Local == local {
			return item.Value
		}
	}
	return ""
}

func validateContentTypes(ctx context.Context, raw []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	depth, nodes := 0, 0
	rootSeen := false
	document, styles := false, false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrInput
		}
		switch item := token.(type) {
		case xml.Directive:
			return ErrInput
		case xml.StartElement:
			depth++
			nodes++
			if nodes > 4096 {
				return ErrLimit
			}
			if depth == 1 {
				if rootSeen || item.Name.Space != contentTypesNS || item.Name.Local != "Types" {
					return ErrInput
				}
				rootSeen = true
			}
			if depth == 2 {
				if item.Name.Space != contentTypesNS || item.Name.Local != "Default" && item.Name.Local != "Override" {
					return ErrInput
				}
				if item.Name.Local == "Override" {
					switch opcAttribute(item, "PartName") {
					case "/word/document.xml":
						if document || opcAttribute(item, "ContentType") != documentContentType {
							return ErrInput
						}
						document = true
					case "/word/styles.xml":
						if styles || opcAttribute(item, "ContentType") != stylesContentType {
							return ErrInput
						}
						styles = true
					}
				}
			}
			if depth > 2 {
				return ErrInput
			}
		case xml.CharData:
			if strings.TrimSpace(string(item)) != "" {
				return ErrInput
			}
		case xml.EndElement:
			depth--
			if depth < 0 {
				return ErrInput
			}
		}
	}
	if depth != 0 || !rootSeen || !document || !styles {
		return ErrInput
	}
	return nil
}

func validateRelationships(ctx context.Context, raw []byte, requiredType, requiredTarget string) error {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	depth, nodes := 0, 0
	rootSeen := false
	found := false
	ids := map[string]struct{}{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrInput
		}
		switch item := token.(type) {
		case xml.Directive:
			return ErrInput
		case xml.StartElement:
			depth++
			nodes++
			if nodes > 4096 {
				return ErrLimit
			}
			if depth == 1 {
				if rootSeen || item.Name.Space != packageRelationshipsNS || item.Name.Local != "Relationships" {
					return ErrInput
				}
				rootSeen = true
			}
			if depth == 2 {
				if item.Name.Space != packageRelationshipsNS || item.Name.Local != "Relationship" {
					return ErrInput
				}
				id, relationType, target := opcAttribute(item, "Id"), opcAttribute(item, "Type"), opcAttribute(item, "Target")
				if id == "" || relationType == "" || target == "" {
					return ErrInput
				}
				if _, duplicate := ids[id]; duplicate {
					return ErrInput
				}
				ids[id] = struct{}{}
				if relationType == requiredType {
					if found || target != requiredTarget ||
						opcAttribute(item, "TargetMode") != "" {
						return ErrInput
					}
					found = true
				}
			}
			if depth > 2 {
				return ErrInput
			}
		case xml.CharData:
			if strings.TrimSpace(string(item)) != "" {
				return ErrInput
			}
		case xml.EndElement:
			depth--
			if depth < 0 {
				return ErrInput
			}
		}
	}
	if depth != 0 || !rootSeen || !found {
		return ErrInput
	}
	return nil
}

func readPart(ctx context.Context, part *zip.File, limit int64) ([]byte, error) {
	if part.UncompressedSize64 > uint64(limit) {
		return nil, ErrLimit
	}
	reader, err := part.Open()
	if err != nil {
		return nil, ErrInput
	}
	raw, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return nil, ErrInput
	}
	if int64(len(raw)) > limit {
		return nil, ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

func word(element xml.Name, local string) bool {
	return element.Space == wordNS && element.Local == local
}

func wordAttribute(element xml.StartElement, local string) string {
	for _, item := range element.Attr {
		if word(item.Name, local) {
			return item.Value
		}
	}
	return ""
}

func readStyleNames(ctx context.Context, raw []byte) (map[string]string, error) {
	names := map[string]string{}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	depth, nodes, currentDepth := 0, 0, 0
	rootSeen := false
	currentID := ""
	defaultID := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrInput
		}
		switch item := token.(type) {
		case xml.Directive:
			return nil, ErrInput
		case xml.StartElement:
			depth++
			nodes++
			if depth == 1 {
				if rootSeen || !word(item.Name, "styles") {
					return nil, ErrInput
				}
				rootSeen = true
			}
			if depth > 64 || nodes > maxDocumentNodes {
				return nil, ErrLimit
			}
			if word(item.Name, "style") {
				if depth != 2 {
					return nil, ErrInput
				}
				currentID, currentDepth = "", 0
				if wordAttribute(item, "type") == "paragraph" {
					currentID, currentDepth = wordAttribute(item, "styleId"), depth
				}
				if currentID != "" && wordAttribute(item, "default") == "1" {
					if defaultID != "" {
						return nil, ErrInput
					}
					defaultID = currentID
				}
			}
			if currentID != "" && word(item.Name, "name") && depth == currentDepth+1 {
				if name := wordAttribute(item, "val"); name != "" {
					// python-docx exposes built-in heading names in their UI
					// spelling even when styles.xml stores "heading 1".
					if strings.HasPrefix(name, "heading ") {
						name = "Heading " + strings.TrimPrefix(name, "heading ")
					}
					if _, exists := names[currentID]; exists {
						return nil, ErrInput
					}
					names[currentID] = name
				}
			}
		case xml.EndElement:
			if currentDepth == depth && word(item.Name, "style") {
				currentID, currentDepth = "", 0
			}
			depth--
			if depth < 0 {
				return nil, ErrInput
			}
		}
	}
	if depth != 0 || !rootSeen {
		return nil, ErrInput
	}
	if defaultID == "" || names[defaultID] == "" {
		return nil, ErrInput
	}
	names[""] = names[defaultID]
	return names, nil
}

type paragraph struct {
	text       strings.Builder
	styleID    string
	listNumber bool
	cell       bool
}

func readDocumentBlocks(ctx context.Context, raw []byte, styles map[string]string) ([]Block, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	blocks := []Block{}
	stack := []xml.Name{}
	var current *paragraph
	textDepth, tableDepth, cellDepth, nodes := 0, 0, 0, 0
	seenDocument, seenBody := false, false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrInput
		}
		switch item := token.(type) {
		case xml.Directive:
			return nil, ErrInput
		case xml.StartElement:
			nodes++
			if nodes > maxDocumentNodes || len(stack) >= 64 {
				return nil, ErrLimit
			}
			parent := xml.Name{}
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			} else {
				if seenDocument || !word(item.Name, "document") {
					return nil, ErrInput
				}
				seenDocument = true
			}
			if word(parent, "document") && !word(item.Name, "body") ||
				word(parent, "body") && !word(item.Name, "p") && !word(item.Name, "tbl") && !word(item.Name, "sectPr") ||
				word(parent, "tbl") && !word(item.Name, "tblPr") && !word(item.Name, "tblGrid") && !word(item.Name, "tr") ||
				word(parent, "tr") && !word(item.Name, "trPr") && !word(item.Name, "tc") ||
				word(parent, "tc") && !word(item.Name, "tcPr") && !word(item.Name, "p") && !word(item.Name, "tbl") ||
				word(parent, "p") && !word(item.Name, "pPr") && !word(item.Name, "r") && !word(item.Name, "hyperlink") ||
				word(parent, "hyperlink") && !word(item.Name, "r") ||
				word(parent, "r") && !word(item.Name, "rPr") && !word(item.Name, "t") &&
					!word(item.Name, "tab") && !word(item.Name, "br") && !word(item.Name, "cr") {
				return nil, ErrInput
			}
			stack = append(stack, item.Name)
			// These bodies can hold visible prose outside python-docx's
			// ordinary paragraph/table traversal. Reject rather than silently
			// publish a report that omitted part of the resume.
			if word(item.Name, "altChunk") || word(item.Name, "txbxContent") ||
				word(item.Name, "sdt") || word(item.Name, "subDoc") {
				return nil, ErrInput
			}
			if word(item.Name, "body") {
				if !word(parent, "document") || seenBody {
					return nil, ErrInput
				}
				seenBody = true
			}
			if word(item.Name, "tbl") {
				tableDepth++
				if tableDepth > 1 {
					return nil, ErrInput
				}
			}
			if word(item.Name, "tc") {
				cellDepth++
			}
			if cellDepth > 0 && (word(item.Name, "vMerge") || word(item.Name, "hMerge") ||
				word(item.Name, "gridSpan") && wordAttribute(item, "val") != "1") {
				// python-docx row.cells duplicates a merged grid position;
				// physical tc traversal would otherwise lose source blocks.
				return nil, ErrInput
			}
			if word(item.Name, "p") && (word(parent, "body") || word(parent, "tc")) && tableDepth <= 1 {
				if current != nil {
					return nil, ErrInput
				}
				if len(blocks) >= 10000 {
					return nil, ErrLimit
				}
				current = &paragraph{cell: cellDepth > 0}
			}
			if word(item.Name, "t") || word(item.Name, "tab") || word(item.Name, "br") || word(item.Name, "cr") {
				if current == nil || !word(parent, "r") {
					return nil, ErrInput
				}
			}
			if current != nil {
				if word(item.Name, "pStyle") {
					if !word(parent, "pPr") {
						return nil, ErrInput
					}
					current.styleID = wordAttribute(item, "val")
				}
				if word(item.Name, "numPr") {
					if !word(parent, "pPr") {
						return nil, ErrInput
					}
					current.listNumber = true
				}
				if word(item.Name, "t") {
					textDepth = len(stack)
				}
				if word(item.Name, "tab") {
					current.text.WriteByte('\t')
				}
				if word(item.Name, "br") || word(item.Name, "cr") {
					current.text.WriteByte('\n')
				}
			}
		case xml.CharData:
			if current != nil && textDepth > 0 {
				current.text.Write(item)
				if current.text.Len() > 4<<20 {
					return nil, ErrLimit
				}
			} else if strings.TrimSpace(string(item)) != "" {
				return nil, ErrInput
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != item.Name {
				return nil, ErrInput
			}
			if current != nil && word(item.Name, "p") {
				block := Block{ID: blockID(len(blocks)), Order: len(blocks)}
				block.Text = textcompat.Strip(current.text.String())
				block.Kind = "paragraph"
				if current.cell {
					block.Kind = "table_cell"
				}
				if block.Text == "" {
					block.Kind = "blank"
				}
				style := "Normal"
				if named := styles[""]; named != "" {
					style = named
				}
				if current.styleID != "" {
					if named, ok := styles[current.styleID]; ok {
						style = named
					} else {
						return nil, ErrInput
					}
				}
				block.Style = &style
				block.IsBullet = bulletPrefix(block.Text) > 0 || strings.HasPrefix(strings.ToLower(style), "list") || current.listNumber
				blocks = append(blocks, block)
				current = nil
			}
			if textDepth == len(stack) && word(item.Name, "t") {
				textDepth = 0
			}
			if word(item.Name, "tc") {
				cellDepth--
			}
			if word(item.Name, "tbl") {
				tableDepth--
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) != 0 || !seenDocument || !seenBody || current != nil || tableDepth != 0 || cellDepth != 0 {
		return nil, ErrInput
	}
	for _, block := range blocks {
		if !utf8.ValidString(block.Text) {
			return nil, ErrInput
		}
	}
	return blocks, nil
}
