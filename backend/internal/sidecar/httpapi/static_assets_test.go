package httpapi

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Comparing Go and Python inventories alone cannot detect an asset missing
// from both. Read the actual first-party assets and check their declared static
// imports too: one missing module prevents app.js from initializing at all.
func TestApplicationStaticAssetsCoverFrontendImports(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../../../../src/intake_ui_static")
	patterns := map[string]*regexp.Regexp{
		".js":   regexp.MustCompile(`(?m)^\s*(?:import|export)\s+(?:[^;"']*?\sfrom\s+)?["']([^"']+)["']`),
		".css":  regexp.MustCompile(`@import\s+(?:url\(\s*)?["']([^"']+)["']`),
		".html": regexp.MustCompile(`(?:src|href)=["'](/[^"'#?]+\.(?:js|css|svg))["']`),
	}
	imports := 0
	for route, asset := range staticFiles {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(asset.Name)))
		if err != nil {
			t.Fatalf("listed asset %s is unavailable: %v", route, err)
		}
		pattern := patterns[path.Ext(asset.Name)]
		if pattern == nil {
			continue
		}
		for _, match := range pattern.FindAllStringSubmatch(string(raw), -1) {
			imports++
			target := match[1]
			if !strings.HasPrefix(target, "/") {
				if !strings.HasPrefix(target, ".") {
					t.Errorf("unbundled import %q in %s", target, asset.Name)
					continue
				}
				target = path.Join("/", path.Dir(asset.Name), target)
			}
			if _, found := staticFiles[target]; !found {
				t.Errorf("frontend %s imports unserved asset %s", asset.Name, target)
			}
		}
	}
	if imports < 20 {
		t.Fatalf("frontend import coverage unexpectedly small: %d", imports)
	}
}
