//go:build darwin

package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Builds a tiny ad-hoc-signed .app, packs it into a dmg, serves that, and
// checks Stage mounts, copies and verifies it.
func TestStageFromDMG(t *testing.T) {
	work := t.TempDir()
	src := filepath.Join(work, "dmgroot", "Fake.app")
	macos := filepath.Join(src, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>fake</string><key>CFBundleIdentifier</key><string>test.fake</string></dict></plist>`
	os.WriteFile(filepath.Join(src, "Contents", "Info.plist"), []byte(plist), 0o644)
	if out, err := exec.Command("/bin/cp", "/usr/bin/true", filepath.Join(macos, "fake")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if out, err := exec.Command("/usr/bin/codesign", "-s", "-", "--force", src).CombinedOutput(); err != nil {
		t.Skipf("cannot ad-hoc sign here: %v %s", err, out)
	}
	dmg := filepath.Join(work, "fake.dmg")
	if out, err := exec.Command("/usr/bin/hdiutil", "create", "-volname", "Fake", "-srcfolder", filepath.Dir(src), "-ov", "-format", "UDZO", dmg).CombinedOutput(); err != nil {
		t.Skipf("cannot build dmg here: %v %s", err, out)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(work)))
	defer srv.Close()

	installed := filepath.Join(t.TempDir(), "Fake.app")
	staged, err := Stage(context.Background(), srv.URL+"/fake.dmg", installed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staged, "Contents", "MacOS", "fake")); err != nil {
		t.Fatalf("staged app is missing its binary: %v", err)
	}
	if _, err := Stage(context.Background(), srv.URL+"/missing.dmg", installed); err == nil {
		t.Error("a 404 download should fail")
	}
}

func TestParseTeamID(t *testing.T) {
	signed := "Identifier=com.example.app\nAuthority=Developer ID Application: Example (ABCDE12345)\nTeamIdentifier=ABCDE12345\n"
	if got := parseTeamID(signed); got != "ABCDE12345" {
		t.Fatalf("parseTeamID = %q", got)
	}
	if got := parseTeamID("Signature=adhoc\nTeamIdentifier=not set\n"); got != "" {
		t.Fatalf("ad hoc should have no team, got %q", got)
	}
}
