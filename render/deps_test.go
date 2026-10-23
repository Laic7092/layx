package render

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCoreImports is the M0 dependency gate: the core package may not directly
// import container, network, HTML/CSS or image-decoding packages. Transitive
// dependencies of go-text/typesetting/font are tracked separately because
// that module pulls image and XML support for color/bitmap fonts.
func TestCoreImports(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{range .Imports}}{{.}}\n{{end}}", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	forbidden := []string{
		"archive/zip",
		"encoding/json",
		"encoding/xml",
		"net/http",
		"regexp",
		"reflect",
		"image",
		"image/jpeg",
		"image/png",
		"golang.org/x/net/html",
	}
	imports := strings.Fields(string(out))
	for _, imp := range imports {
		for _, f := range forbidden {
			if imp == f || strings.HasPrefix(imp, f+"/") {
				t.Fatalf("core imports forbidden package %q", imp)
			}
		}
	}
}
