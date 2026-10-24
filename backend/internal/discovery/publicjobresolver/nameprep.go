package publicjobresolver

import (
	_ "embed"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

// Python's IDNA codec is IDNA2003, not the evolving UTS46 lookup profile.
// Frozen stringprep/Unicode3.2 properties and normalization corrections preserve
// that contract; raw RFC3492 encoding uses the already-pinned x/net dependency.
//
//go:embed testdata/nameprep32.json
var nameprepData []byte
var nameprepTable = func() struct {
	Unicode     string
	Mapping     map[string]string
	Ranges      [][4]int
	Corrections map[string]string
} {
	var value struct {
		Unicode     string
		Mapping     map[string]string
		Ranges      [][4]int
		Corrections map[string]string
	}
	if json.Unmarshal(nameprepData, &value) != nil || value.Unicode != "3.2.0" {
		panic("invalid embedded nameprep tables")
	}
	return value
}()
var errHostname = errors.New("unsupported hostname")

func nameprepProperties(r rune) (int, uint8) {
	i := sort.Search(len(nameprepTable.Ranges), func(i int) bool { return nameprepTable.Ranges[i][1] >= int(r) })
	if i < len(nameprepTable.Ranges) && nameprepTable.Ranges[i][0] <= int(r) {
		return nameprepTable.Ranges[i][2], uint8(nameprepTable.Ranges[i][3])
	}
	return 0, 0
}
func hostnameASCII(host string) (string, error) {
	host = strings.NewReplacer("\u3002", ".", "\uff0e", ".", "\uff61", ".").Replace(host)
	labels := strings.Split(host, ".")
	for i, label := range labels {
		if strings.ContainsFunc(label, func(r rune) bool { return r >= 128 }) {
			var err error
			label, err = nameprep(label)
			if err != nil {
				return "", err
			}
			if strings.ContainsFunc(label, func(r rune) bool { return r >= 128 }) {
				if strings.HasPrefix(label, "xn--") {
					return "", errHostname
				}
				label, err = idna.Punycode.ToASCII(label)
				if err != nil {
					return "", errHostname
				}
			}
		}
		if len(label) == 0 || len(label) > 63 {
			return "", errHostname
		}
		labels[i] = strings.ToLower(label)
	}
	return strings.Join(labels, "."), nil
}

func nameprep(label string) (string, error) {
	if norm.Version != "17.0.0" {
		return "", errHostname
	}
	type point struct {
		r rune
		c uint8
	}
	var points []point
	for _, r := range label {
		mapped, ok := nameprepTable.Mapping[strconv.Itoa(int(r))]
		if !ok {
			mapped = string(r)
		}
		for _, m := range mapped {
			flags, _ := nameprepProperties(m)
			decomposition := string(m)
			if flags&1 != 0 {
				if fixed, ok := nameprepTable.Corrections[strconv.Itoa(int(m))]; ok {
					decomposition = fixed
				} else {
					decomposition = norm.NFKD.String(decomposition)
				}
			}
			for _, d := range decomposition {
				_, c := nameprepProperties(d)
				points = append(points, point{d, c})
			}
		}
	}
	for first := 0; first < len(points); {
		if points[first].c == 0 {
			first++
			continue
		}
		last := first + 1
		for last < len(points) && points[last].c != 0 {
			last++
		}
		sort.SliceStable(points[first:last], func(i, j int) bool { return points[first+i].c < points[first+j].c })
		first = last
	}
	count, starter, lastClass := 0, -1, uint8(0)
	for _, current := range points {
		if starter >= 0 && (lastClass < current.c || lastClass == 0) {
			left, _ := nameprepProperties(points[starter].r)
			right, _ := nameprepProperties(current.r)
			if left&1 != 0 && right&1 != 0 && !norm.NFC.PropertiesString(string(current.r)).BoundaryBefore() {
				pair := norm.NFC.String(string([]rune{points[starter].r, current.r}))
				if utf8.RuneCountInString(pair) == 1 {
					r, _ := utf8.DecodeRuneInString(pair)
					flags, _ := nameprepProperties(r)
					if flags&1 != 0 {
						points[starter].r = r
						continue
					}
				}
			}
		}
		if current.c == 0 {
			starter = count
		}
		points[count] = current
		count++
		lastClass = current.c
	}
	points = points[:count]
	var b strings.Builder
	randAL, lcat := false, false
	for _, p := range points {
		flags, _ := nameprepProperties(p.r)
		if flags&2 != 0 {
			return "", errHostname
		}
		randAL = randAL || flags&4 != 0
		lcat = lcat || flags&8 != 0
		b.WriteRune(p.r)
	}
	if randAL {
		first, _ := nameprepProperties(points[0].r)
		last, _ := nameprepProperties(points[len(points)-1].r)
		if lcat || first&4 == 0 || last&4 == 0 {
			return "", errHostname
		}
	}
	return b.String(), nil
}
