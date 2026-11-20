package skills

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lavluda/cd-switcher/internal/harness"
)

func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	old := harness.Home
	harness.Home = func() string { return h }
	t.Cleanup(func() { harness.Home = old })
	return h
}

func tarball(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestInstallFromGitHubLinksForClaude(t *testing.T) {
	h := home(t)
	archive := tarball(t, map[string]string{
		"skills-abc/README.md":               "x",
		"skills-abc/skills/pdf/SKILL.md":     "---\nname: pdf\n---",
		"skills-abc/skills/pdf/scripts/a.py": "print()",
		"skills-abc/skills/xlsx/SKILL.md":    "---\nname: xlsx\n---",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/anthropics/skills/tar.gz/HEAD" {
			w.WriteHeader(404)
			return
		}
		w.Write(archive)
	}))
	defer srv.Close()
	codeload = srv.URL

	src, err := ParseSource("https://github.com/anthropics/skills/tree/HEAD/skills")
	if err != nil || src.Path != "skills" || src.Ref != "HEAD" {
		t.Fatalf("source = %+v %v", src, err)
	}
	names, err := InstallGitHub(context.Background(), src)
	if err != nil || len(names) != 2 {
		t.Fatalf("names = %v err = %v", names, err)
	}
	link := filepath.Join(h, ".claude", "skills", "pdf")
	if target, _ := os.Readlink(link); target != filepath.Join(h, ".agents", "skills", "pdf") {
		t.Fatalf("claude link = %q", target)
	}
	if _, err := os.Stat(filepath.Join(h, ".agents", "skills", "pdf", "scripts", "a.py")); err != nil {
		t.Fatal("nested file missing")
	}
	list := List()
	if len(list) != 2 || !list[0].Linked || !list[0].Shared {
		t.Fatalf("list = %+v", list)
	}
	if err := Remove("pdf", filepath.Join(h, "backups")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) || len(List()) != 1 {
		t.Fatal("remove left the skill behind")
	}
}

func TestShareMovesClaudeOnlySkill(t *testing.T) {
	h := home(t)
	own := filepath.Join(h, ".claude", "skills", "mine")
	os.MkdirAll(own, 0o755)
	os.WriteFile(filepath.Join(own, "SKILL.md"), []byte("x"), 0o644)
	if err := Share("mine"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(own); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("not linked back")
	}
	if l := List(); len(l) != 1 || !l[0].Shared || !l[0].Linked {
		t.Fatalf("list = %+v", l)
	}
}

func TestZipRejectsEscapingPaths(t *testing.T) {
	home(t)
	p := filepath.Join(t.TempDir(), "evil.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../../escape/SKILL.md")
	w.Write([]byte("x"))
	zw.Close()
	f.Close()
	if _, err := InstallZip(p); err == nil {
		t.Fatal("expected an unsafe-path error")
	}
}
