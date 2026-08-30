package detector

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestScanTree(t *testing.T) {
	spec := os.Getenv("TREES")
	if spec == "" {
		t.Skip()
	}
	d := New(Config{Locales: []string{"fr", "gb", "us"}})

	var out strings.Builder
	grand := 0
	for _, root := range strings.Split(spec, ",") {
		var files []string
		filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
			if e != nil || i.IsDir() {
				return nil
			}
			if strings.Contains(p, "/node_modules/") || strings.Contains(p, "/dist/") {
				return nil
			}
			switch filepath.Ext(p) {
			case ".go", ".ts", ".js", ".mjs", ".json", ".html", ".md", ".sh", ".yml", ".yaml":
				files = append(files, p)
			}
			return nil
		})
		sort.Strings(files)

		n := 0
		var b strings.Builder
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, m := range d.Scan(string(raw)) {
				n++
				ln := 1 + strings.Count(string(raw[:m.Start]), "\n")
				v := strings.ReplaceAll(m.Value, "\n", "\\n")
				if len(v) > 56 {
					v = v[:56] + "…"
				}
				fmt.Fprintf(&b, "  %-42s L%-5d %-16s %s\n",
					strings.TrimPrefix(f, root+"/"), ln, m.Category, v)
			}
		}
		grand += n
		fmt.Fprintf(&out, "\n===== %s — %d files, %d finding(s)\n%s", root, len(files), n, b.String())
	}
	fmt.Fprintf(&out, "\nTOTAL %d\n", grand)
	os.WriteFile("/tmp/tree.txt", []byte(out.String()), 0o600)
}
