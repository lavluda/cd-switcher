// Package envcheck finds environment variables that override what CD-Switcher
// sets. A shell profile that exports ANTHROPIC_BASE_URL, for example, beats
// the provider chosen in the menu for every terminal session.
package envcheck

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/cfgfile"
	"github.com/lavluda/cd-switcher/internal/harness"
)

// Finding is one place a conflicting variable is set.
type Finding struct {
	Name string
	File string // "" for the running environment, "registry" on Windows
	Line int    // 1-based; 0 when not from a file
}

func (f Finding) String() string {
	switch {
	case f.File == "":
		return f.Name + " in the environment"
	case f.Line == 0:
		return fmt.Sprintf("%s in %s", f.Name, f.File)
	}
	return fmt.Sprintf("%s in %s line %d", f.Name, f.File, f.Line)
}

var watched = regexp.MustCompile(`^(ANTHROPIC_[A-Z0-9_]+|OPENAI_[A-Z0-9_]+|CODEX_HOME|CLAUDE_CONFIG_DIR|CLAUDE_CODE_USE_(BEDROCK|VERTEX|FOUNDRY))$`)

// assignment matches "export NAME=", "NAME=", "set -gx NAME" and
// "setenv NAME" at the start of a line.
var assignment = regexp.MustCompile(`^\s*(?:export\s+|declare\s+-x\s+)?([A-Za-z_][A-Za-z0-9_]*)=|^\s*set\s+(?:-[a-zA-Z]+\s+)*([A-Za-z_][A-Za-z0-9_]*)\s|^\s*setenv\s+([A-Za-z_][A-Za-z0-9_]*)\s`)

// RCFiles are the shell startup files checked.
func RCFiles() []string {
	h := harness.Home()
	return []string{
		filepath.Join(h, ".zshenv"), filepath.Join(h, ".zprofile"), filepath.Join(h, ".zshrc"),
		filepath.Join(h, ".bash_profile"), filepath.Join(h, ".bashrc"), filepath.Join(h, ".profile"),
		filepath.Join(h, ".config", "fish", "config.fish"),
	}
}

// Scan reports every watched variable set in the environment, a shell
// startup file, or (on Windows) the user's registry environment.
func Scan() []Finding {
	var out []Finding
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if watched.MatchString(name) {
			out = append(out, Finding{Name: name})
		}
	}
	for _, f := range RCFiles() {
		out = append(out, scanFile(f)...)
	}
	if runtime.GOOS == "windows" {
		out = append(out, scanRegistry()...)
	}
	return out
}

func scanFile(path string) []Finding {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Finding
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := assignment.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1] + m[2] + m[3]
		if watched.MatchString(name) {
			out = append(out, Finding{Name: name, File: path, Line: n})
		}
	}
	return out
}

func scanRegistry() []Finding {
	out, err := exec.Command("reg", "query", `HKCU\Environment`).Output()
	if err != nil {
		return nil
	}
	var fs []Finding
	for _, l := range strings.Split(string(out), "\n") {
		fields := strings.Fields(l)
		if len(fields) >= 2 && strings.HasPrefix(fields[1], "REG_") && watched.MatchString(fields[0]) {
			fs = append(fs, Finding{Name: fields[0], File: "registry"})
		}
	}
	return fs
}

// Disable comments out the line a finding points at, after copying the file
// to backupDir. It only touches a line that still sets the same variable.
func Disable(f Finding, backupDir string) error {
	if f.File == "" || f.Line == 0 {
		return fmt.Errorf("%s is not set in a file CD-Switcher can edit", f.Name)
	}
	b, err := os.ReadFile(f.File)
	if err != nil {
		return err
	}
	ls := strings.Split(string(b), "\n")
	if f.Line > len(ls) {
		return fmt.Errorf("%s changed since it was checked", f.File)
	}
	m := assignment.FindStringSubmatch(ls[f.Line-1])
	if m == nil || m[1]+m[2]+m[3] != f.Name {
		return fmt.Errorf("%s changed since it was checked", f.File)
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	backup := filepath.Join(backupDir, filepath.Base(f.File)+"."+time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backup, b, 0o600); err != nil {
		return err
	}
	ls[f.Line-1] = "# disabled by CD-Switcher: " + ls[f.Line-1]
	return cfgfile.WriteFile(f.File, []byte(strings.Join(ls, "\n")), false)
}
