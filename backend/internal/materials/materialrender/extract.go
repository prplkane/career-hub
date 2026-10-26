package materialrender

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"slices"
	"strings"

	"careerhub.local/backend/internal/jobs/joburls"
)

type xmlNode struct {
	name     xml.Name
	attrs    map[xml.Name]string
	children []*xmlNode
	text     strings.Builder
}

func invalidDOCX() *Error { return fail("MATERIAL_RENDER_DOCX_INVALID") }

func parseXML(ctx context.Context, raw []byte) (*xmlNode, *Error) {
	if len(raw) == 0 || len(raw) > MaxTextBytes || !validXMLText(string(raw)) {
		return nil, invalidDOCX()
	}
	d := xml.NewDecoder(bytes.NewReader(raw))
	var root *xmlNode
	stack := []*xmlNode{}
	count, tokens := 0, 0
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, invalidDOCX()
		}
		tokens++
		if tokens%256 == 0 {
			if e := ready(ctx); e != nil {
				return nil, e
			}
		}
		if tokens > 160000 {
			return nil, invalidDOCX()
		}
		switch x := t.(type) {
		case xml.StartElement:
			count++
			if count > 50000 || len(stack) >= 16 || len(x.Attr) > 16 {
				return nil, invalidDOCX()
			}
			n := &xmlNode{name: x.Name, attrs: map[xml.Name]string{}}
			for _, a := range x.Attr {
				if _, duplicate := n.attrs[a.Name]; duplicate {
					return nil, invalidDOCX()
				}
				n.attrs[a.Name] = a.Value
			}
			// Namespace declarations do not authorize any unknown elements/attrs.
			for name := range n.attrs {
				if name.Space == "xmlns" || name.Space == "" && name.Local == "xmlns" {
					delete(n.attrs, name)
				}
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, invalidDOCX()
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != x.Name {
				return nil, invalidDOCX()
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(x)) != "" {
					return nil, invalidDOCX()
				}
			} else {
				stack[len(stack)-1].text.Write(x)
			}
		case xml.ProcInst:
			if x.Target != "xml" || tokens != 1 {
				return nil, invalidDOCX()
			}
		default: // Includes comments, DTDs and entity declarations.
			return nil, invalidDOCX()
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, invalidDOCX()
	}
	if e := ready(ctx); e != nil {
		return nil, e
	}
	return root, nil
}

func (n *xmlNode) is(ns, local string) bool {
	return n != nil && n.name == (xml.Name{Space: ns, Local: local})
}
func (n *xmlNode) emptyText() bool { return strings.TrimSpace(n.text.String()) == "" }
func (n *xmlNode) attrsAre(ns string, expected map[string]string) bool {
	if len(n.attrs) != len(expected) {
		return false
	}
	for key, value := range expected {
		if actual, ok := n.attrs[xml.Name{Space: ns, Local: key}]; !ok || actual != value {
			return false
		}
	}
	return true
}
func (n *xmlNode) leaf(ns string, expected map[string]string) bool {
	return len(n.children) == 0 && n.emptyText() && n.attrsAre(ns, expected)
}

func readDOCXParts(ctx context.Context, raw []byte) (map[string][]byte, *Error) {
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(z.File) != 9 || z.Comment != "" {
		return nil, invalidDOCX()
	}
	allowed := []string{"[Content_Types].xml", "_rels/.rels", "docProps/core.xml", "word/document.xml", "word/_rels/document.xml.rels", "word/styles.xml", "word/numbering.xml", "word/settings.xml", "word/theme/theme1.xml"}
	parts := make(map[string][]byte, len(allowed))
	total := 0
	for _, f := range z.File {
		if e := ready(ctx); e != nil {
			return nil, e
		}
		if !slices.Contains(allowed, f.Name) || parts[f.Name] != nil || f.UncompressedSize64 > MaxTextBytes || f.Flags&1 != 0 {
			return nil, invalidDOCX()
		}
		total += int(f.UncompressedSize64)
		if total > 4<<20 {
			return nil, invalidDOCX()
		}
		r, err := f.Open()
		if err != nil {
			return nil, invalidDOCX()
		}
		var out bytes.Buffer
		buf := make([]byte, 32<<10)
		for {
			if e := ready(ctx); e != nil {
				_ = r.Close()
				return nil, e
			}
			n, err := r.Read(buf)
			if out.Len()+n > MaxTextBytes {
				_ = r.Close()
				return nil, invalidDOCX()
			}
			out.Write(buf[:n])
			if err == io.EOF {
				break
			}
			if err != nil {
				_ = r.Close()
				return nil, invalidDOCX()
			}
		}
		if r.Close() != nil || out.Len() != int(f.UncompressedSize64) {
			return nil, invalidDOCX()
		}
		parts[f.Name] = out.Bytes()
	}
	for name, expected := range map[string]string{"[Content_Types].xml": contentTypes, "_rels/.rels": packageRelationships, "word/numbering.xml": numberingXML, "word/settings.xml": settingsXML, "word/theme/theme1.xml": themeXML} {
		if string(parts[name]) != expected {
			return nil, invalidDOCX()
		}
	}
	styles := string(parts["word/styles.xml"])
	if styles != stylesXML(20) && styles != stylesXML(21) {
		return nil, invalidDOCX()
	}
	return parts, nil
}

func readRelationships(ctx context.Context, raw []byte) (map[string]bool, *Error) {
	n, e := parseXML(ctx, raw)
	if e != nil {
		return nil, e
	}
	if !n.is(pkgNS, "Relationships") || len(n.attrs) != 0 || !n.emptyText() || len(n.children) < 4 || len(n.children) > 7 {
		return nil, invalidDOCX()
	}
	links := map[string]bool{}
	seen := map[string]bool{}
	for _, r := range n.children {
		if !r.is(pkgNS, "Relationship") || len(r.children) != 0 || !r.emptyText() {
			return nil, invalidDOCX()
		}
		id := r.attrs[xml.Name{Local: "Id"}]
		if id == "" || seen[id] {
			return nil, invalidDOCX()
		}
		seen[id] = true
		if spec, ok := map[string]struct{ name, target string }{"rId1": {"styles", "styles.xml"}, "rId2": {"numbering", "numbering.xml"}, "rId3": {"settings", "settings.xml"}, "rId4": {"theme", "theme/theme1.xml"}}[id]; ok {
			if !r.attrsAre("", map[string]string{"Id": id, "Type": rNS + "/" + spec.name, "Target": spec.target}) {
				return nil, invalidDOCX()
			}
			continue
		}
		if !slices.Contains([]string{"rId5", "rId6", "rId7"}, id) {
			return nil, invalidDOCX()
		}
		target := r.attrs[xml.Name{Local: "Target"}]
		if !r.attrsAre("", map[string]string{"Id": id, "Type": rNS + "/hyperlink", "Target": target, "TargetMode": "External"}) {
			return nil, invalidDOCX()
		}
		valid, problem := joburls.ValidSource(target)
		if problem != nil || !valid {
			return nil, invalidDOCX()
		}
		links[id] = true
	}
	if !seen["rId1"] || !seen["rId2"] || !seen["rId3"] || !seen["rId4"] {
		return nil, invalidDOCX()
	}
	return links, nil
}

func validateCore(ctx context.Context, raw []byte) *Error {
	if len(raw) > 8192 {
		return invalidDOCX()
	}
	n, e := parseXML(ctx, raw)
	if e != nil {
		return e
	}
	cp, dc := "http://schemas.openxmlformats.org/package/2006/metadata/core-properties", "http://purl.org/dc/elements/1.1/"
	if !n.is(cp, "coreProperties") || len(n.attrs) != 0 || !n.emptyText() || len(n.children) != 5 {
		return invalidDOCX()
	}
	for i, spec := range []struct{ ns, name string }{{dc, "title"}, {dc, "subject"}, {dc, "creator"}, {cp, "lastModifiedBy"}, {dc, "description"}} {
		c := n.children[i]
		if !c.is(spec.ns, spec.name) || len(c.attrs) != 0 || len(c.children) != 0 {
			return invalidDOCX()
		}
		if (i == 2 || i == 3) && c.text.Len() != 0 {
			return invalidDOCX()
		}
	}
	return nil
}

func validateProperties(n *xmlNode, runProperties bool) bool {
	if len(n.attrs) != 0 || !n.emptyText() {
		return false
	}
	order := []string{"pStyle", "keepNext", "widowControl", "pBdr", "tabs", "spacing", "ind", "jc"}
	if runProperties {
		order = []string{"rFonts", "b", "i", "color", "sz", "szCs", "u"}
	}
	last := -1
	for _, child := range n.children {
		i := slices.Index(order, child.name.Local)
		if child.name.Space != wNS || i <= last {
			return false
		}
		last = i
		val := child.attrs[xml.Name{Space: wNS, Local: "val"}]
		switch child.name.Local {
		case "pStyle":
			if !child.leaf(wNS, map[string]string{"val": "CareerHubResumeBullet"}) {
				return false
			}
		case "keepNext", "b", "i":
			if !child.leaf(wNS, nil) && !child.leaf(wNS, map[string]string{"val": "0"}) {
				return false
			}
		case "widowControl":
			if !child.leaf(wNS, nil) {
				return false
			}
		case "pBdr":
			if len(child.children) != 1 || len(child.attrs) != 0 || !child.emptyText() {
				return false
			}
			b := child.children[0]
			size := b.attrs[xml.Name{Space: wNS, Local: "sz"}]
			if !b.is(wNS, "bottom") || size != "6" && size != "12" || !b.leaf(wNS, map[string]string{"val": "single", "sz": size, "space": "1", "color": "000000"}) {
				return false
			}
		case "tabs":
			if len(child.attrs) != 0 || !child.emptyText() || len(child.children) < 1 || len(child.children) > 2 {
				return false
			}
			positions := map[string]bool{}
			for _, t := range child.children {
				pos := t.attrs[xml.Name{Space: wNS, Local: "pos"}]
				align := t.attrs[xml.Name{Space: wNS, Local: "val"}]
				if !t.is(wNS, "tab") || positions[pos] || !(align == "center" && pos == "4410" || align == "right" && (pos == "10771" || pos == "10800")) || !t.leaf(wNS, map[string]string{"val": align, "pos": pos}) {
					return false
				}
				positions[pos] = true
			}
		case "spacing":
			b := child.attrs[xml.Name{Space: wNS, Local: "before"}]
			a := child.attrs[xml.Name{Space: wNS, Local: "after"}]
			l := child.attrs[xml.Name{Space: wNS, Local: "line"}]
			if !slices.Contains([]string{"0", "100"}, b) || !slices.Contains([]string{"0", "20", "40", "200"}, a) || !slices.Contains([]string{"240", "297", "360"}, l) || !child.leaf(wNS, map[string]string{"before": b, "after": a, "line": l, "lineRule": "auto"}) {
				return false
			}
		case "ind":
			if !child.leaf(wNS, map[string]string{"left": "288", "hanging": "288"}) {
				return false
			}
		case "jc":
			if !child.leaf(wNS, map[string]string{"val": "center"}) {
				return false
			}
		case "rFonts":
			if !child.leaf(wNS, map[string]string{"ascii": "Calibri", "hAnsi": "Calibri"}) {
				return false
			}
		case "sz":
			if !slices.Contains([]string{"18", "19", "20", "21", "30", "31"}, val) || !child.leaf(wNS, map[string]string{"val": val}) {
				return false
			}
		case "szCs":
			if !child.leaf(wNS, map[string]string{"val": "20"}) {
				return false
			}
		case "u":
			if !child.leaf(wNS, map[string]string{"val": "single"}) {
				return false
			}
		case "color":
			if !slices.Contains([]string{"0563C1", "1155CC"}, val) || !child.leaf(wNS, map[string]string{"val": val}) {
				return false
			}
		default:
			return false
		}
	}
	if runProperties {
		return len(n.children) >= 2 && n.children[0].is(wNS, "rFonts") && hasChild(n, "sz")
	}
	return hasChild(n, "spacing")
}

func hasChild(n *xmlNode, name string) bool {
	for _, c := range n.children {
		if c.is(wNS, name) {
			return true
		}
	}
	return false
}

func extractRun(n *xmlNode, text *strings.Builder) bool {
	if !n.is(wNS, "r") || len(n.attrs) != 0 || !n.emptyText() || len(n.children) == 0 || !n.children[0].is(wNS, "rPr") || !validateProperties(n.children[0], true) {
		return false
	}
	for _, c := range n.children[1:] {
		if len(c.children) != 0 {
			return false
		}
		switch {
		case c.is(wNS, "t"):
			if !c.attrsAre(xmlNS, map[string]string{"space": "preserve"}) && len(c.attrs) != 0 {
				return false
			}
			text.WriteString(c.text.String())
		case c.is(wNS, "tab"):
			if !c.leaf(wNS, nil) {
				return false
			}
			text.WriteByte('\t')
		case c.is(wNS, "br"):
			if !c.leaf(wNS, nil) {
				return false
			}
			text.WriteByte('\n')
		default:
			return false
		}
	}
	return text.Len() <= MaxTextBytes
}

func validateSection(n *xmlNode) bool {
	if !n.is(wNS, "sectPr") || len(n.attrs) != 0 || !n.emptyText() || len(n.children) != 4 {
		return false
	}
	specs := []struct {
		name  string
		attrs map[string]string
	}{
		{"pgSz", map[string]string{"w": "12240", "h": "15840"}},
		{"pgMar", map[string]string{"top": "720", "right": "720", "bottom": "720", "left": "720", "header": "216", "footer": "216", "gutter": "0"}},
		{"cols", map[string]string{"space": "720"}}, {"docGrid", map[string]string{"linePitch": "360"}},
	}
	for i, s := range specs {
		if !n.children[i].is(wNS, s.name) || !n.children[i].leaf(wNS, s.attrs) {
			return false
		}
	}
	return true
}

func extractDOCX(ctx context.Context, raw []byte) (string, *Error) {
	parts, e := readDOCXParts(ctx, raw)
	if e != nil {
		return "", e
	}
	links, e := readRelationships(ctx, parts["word/_rels/document.xml.rels"])
	if e != nil {
		return "", e
	}
	if e := validateCore(ctx, parts["docProps/core.xml"]); e != nil {
		return "", e
	}
	n, e := parseXML(ctx, parts["word/document.xml"])
	if e != nil {
		return "", e
	}
	if !n.is(wNS, "document") || len(n.attrs) != 0 || !n.emptyText() || len(n.children) != 1 {
		return "", invalidDOCX()
	}
	body := n.children[0]
	if !body.is(wNS, "body") || len(body.attrs) != 0 || !body.emptyText() || len(body.children) < 2 || !validateSection(body.children[len(body.children)-1]) {
		return "", invalidDOCX()
	}
	var text strings.Builder
	used := map[string]bool{}
	for _, p := range body.children[:len(body.children)-1] {
		if e := ready(ctx); e != nil {
			return "", e
		}
		if !p.is(wNS, "p") || len(p.attrs) != 0 || !p.emptyText() || len(p.children) == 0 || !p.children[0].is(wNS, "pPr") || !validateProperties(p.children[0], false) {
			return "", invalidDOCX()
		}
		for _, child := range p.children[1:] {
			if child.is(wNS, "r") {
				if !extractRun(child, &text) {
					return "", invalidDOCX()
				}
				continue
			}
			id := child.attrs[xml.Name{Space: rNS, Local: "id"}]
			if !child.is(wNS, "hyperlink") || !links[id] || !child.attrsAre(rNS, map[string]string{"id": id}) || !child.emptyText() || len(child.children) != 1 || !extractRun(child.children[0], &text) {
				return "", invalidDOCX()
			}
			used[id] = true
		}
		text.WriteByte('\n')
		if text.Len() > MaxTextBytes {
			return "", fail("MATERIAL_RENDER_TEXT_INVALID")
		}
	}
	if len(links) != len(used) {
		return "", invalidDOCX()
	}
	return text.String(), nil
}
