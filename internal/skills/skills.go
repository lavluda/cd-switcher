// Package skills installs agent skills (folders with a SKILL.md) once and
// makes every tool see them.
//
// Master copies live in ~/.agents/skills, which Codex, Gemini CLI, Cursor,
// OpenCode and Qwen Code read directly. Claude Code reads ~/.claude/skills,
// so each skill also gets a symlink there.
package skills

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

// Skill is one installed skill.
type Skill struct {
	Name   string
	Dir    string // the real folder
	Linked bool   // Claude Code sees it
	Shared bool   // lives in ~/.agents/skills, so the other tools see it
}

var (
	codeload   = "https://codeload.github.com"
	httpClient = &http.Client{Timeout: 60 * time.Second}
	validName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

const maxDownload = 64 << 20

func claudeSkills() string { return filepath.Join(harness.ClaudeDir(), "skills") }

// List returns every skill in ~/.agents/skills and ~/.claude/skills.
func List() []Skill {
	byName := map[string]*Skill{}
	for _, root := range []string{harness.AgentsSkills(), claudeSkills()} {
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			p := filepath.Join(root, e.Name())
			real, err := filepath.EvalSymlinks(p)
			if err != nil || !isSkill(real) {
				continue
			}
			s := byName[e.Name()]
			if s == nil {
				s = &Skill{Name: e.Name(), Dir: real}
				byName[e.Name()] = s
			}
			if root == harness.AgentsSkills() {
				s.Shared, s.Dir = true, real
			} else {
				s.Linked = true
			}
		}
	}
	out := make([]Skill, 0, len(byName))
	for _, s := range byName {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func isSkill(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil
}

// Share makes a skill visible to every tool: a skill that lives only in
// ~/.claude/skills moves to ~/.agents/skills and is linked back, and a shared
// skill gets its Claude Code link.
func Share(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("%q is not a skill name", name)
	}
	master := filepath.Join(harness.AgentsSkills(), name)
	link := filepath.Join(claudeSkills(), name)
	if !isSkill(master) {
		fi, err := os.Lstat(link)
		if err != nil || fi.Mode()&fs.ModeSymlink != 0 || !isSkill(link) {
			return fmt.Errorf("%s is not installed", name)
		}
		if err := os.MkdirAll(harness.AgentsSkills(), 0o755); err != nil {
			return err
		}
		if err := os.Rename(link, master); err != nil {
			return err
		}
	}
	return linkClaude(name)
}

func linkClaude(name string) error {
	link := filepath.Join(claudeSkills(), name)
	master := filepath.Join(harness.AgentsSkills(), name)
	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&fs.ModeSymlink != 0 {
			if t, _ := filepath.EvalSymlinks(link); t == mustEval(master) {
				return nil
			}
			return fmt.Errorf("%s already links somewhere else", link)
		}
		return fmt.Errorf("%s already exists as a folder", link)
	}
	if err := os.MkdirAll(claudeSkills(), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(master, link); err != nil {
		// Windows needs Developer Mode for symlinks; a copy works everywhere.
		return copyDir(master, link)
	}
	return nil
}

func mustEval(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Remove moves a skill to backupDir and drops its Claude Code link.
func Remove(name, backupDir string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("%q is not a skill name", name)
	}
	link := filepath.Join(claudeSkills(), name)
	master := filepath.Join(harness.AgentsSkills(), name)
	keep := master
	if !isSkill(master) {
		keep = link // a skill that lives only in Claude Code's folder
	} else if fi, err := os.Lstat(link); err == nil {
		// Claude Code's entry is a link or, where links fail, a copy.
		if fi.Mode()&fs.ModeSymlink != 0 {
			err = os.Remove(link)
		} else {
			err = os.RemoveAll(link)
		}
		if err != nil {
			return err
		}
	}
	if !isSkill(keep) {
		return nil
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	return os.Rename(keep, filepath.Join(backupDir, name+"-"+time.Now().Format("20060102-150405")))
}

// Hash fingerprints a skill folder, to tell whether an update changes it.
func Hash(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(b))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

// Source is a GitHub location: owner/repo[/sub/path][@ref].
type Source struct {
	Owner, Repo, Path, Ref string
}

var sourcePattern = regexp.MustCompile(`^(?:https://github\.com/)?([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)(?:/(?:tree/([^/]+)/)?([^@]*))?(?:@([^/]+))?$`)

// ParseSource reads "owner/repo/path@ref" or a github.com tree URL.
func ParseSource(s string) (Source, error) {
	m := sourcePattern.FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(s), "/"))
	if m == nil {
		return Source{}, errors.New("use owner/repo/path, for example anthropics/skills/skills/pdf")
	}
	src := Source{Owner: m[1], Repo: strings.TrimSuffix(m[2], ".git"), Path: strings.Trim(m[4], "/"), Ref: m[5]}
	if m[3] != "" {
		src.Ref = m[3]
	}
	if src.Ref == "" {
		src.Ref = "HEAD"
	}
	return src, nil
}

func (s Source) String() string {
	out := s.Owner + "/" + s.Repo
	if s.Path != "" {
		out += "/" + s.Path
	}
	if s.Ref != "HEAD" {
		out += "@" + s.Ref
	}
	return out
}

// InstallGitHub downloads a repository archive and installs every skill under
// the source path. It returns the installed names.
func InstallGitHub(ctx context.Context, src Source) ([]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%s/%s/tar.gz/%s", codeload, src.Owner, src.Repo, src.Ref), nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: status %d", src, resp.StatusCode)
	}
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, maxDownload))
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "cd-switcher-skill-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		// Archives start with a "<repo>-<sha>/" folder; drop it.
		_, rel, ok := strings.Cut(h.Name, "/")
		if !ok || h.Typeflag != tar.TypeReg {
			continue
		}
		if err := extract(tmp, rel, tr, h.FileInfo().Mode()); err != nil {
			return nil, err
		}
	}
	return installFrom(filepath.Join(tmp, filepath.FromSlash(src.Path)))
}

// InstallZip installs every skill in a zip file.
func InstallZip(zipPath string) ([]string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tmp, err := os.MkdirTemp("", "cd-switcher-skill-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		total += int64(f.UncompressedSize64)
		if total > maxDownload {
			return nil, errors.New("zip is too large for a skill")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		err = extract(tmp, f.Name, rc, f.Mode())
		rc.Close()
		if err != nil {
			return nil, err
		}
	}
	return installFrom(tmp)
}

// extract writes one archive member under root, refusing any path that would
// land outside it.
func extract(root, name string, r io.Reader, mode fs.FileMode) error {
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." {
			return fmt.Errorf("unsafe path %q in archive", name)
		}
	}
	dst := filepath.Join(root, filepath.FromSlash(path.Clean("/"+filepath.ToSlash(name))))
	if !strings.HasPrefix(dst, filepath.Clean(root)+string(filepath.Separator)) {
		return fmt.Errorf("unsafe path %q in archive", name)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()&0o755|0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(r, maxDownload))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// installFrom installs root itself when it is a skill, otherwise every skill
// folder found below it.
func installFrom(root string) ([]string, error) {
	var dirs []string
	if isSkill(root) {
		dirs = []string{root}
	} else {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && isSkill(p) {
				dirs = append(dirs, p)
				return filepath.SkipDir
			}
			return nil
		})
	}
	if len(dirs) == 0 {
		return nil, errors.New("no SKILL.md found there")
	}
	var names []string
	for _, d := range dirs {
		name := filepath.Base(d)
		if !validName.MatchString(name) {
			continue
		}
		if err := place(d, name); err != nil {
			return names, err
		}
		names = append(names, name)
	}
	return names, nil
}

// place puts a skill folder at ~/.agents/skills/<name>, replacing an older
// copy, and links it for Claude Code.
func place(src, name string) error {
	if err := os.MkdirAll(harness.AgentsSkills(), 0o755); err != nil {
		return err
	}
	dst := filepath.Join(harness.AgentsSkills(), name)
	staged := dst + ".cdswitcher-new"
	_ = os.RemoveAll(staged)
	if err := copyDir(src, staged); err != nil {
		_ = os.RemoveAll(staged)
		return err
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(staged, dst); err != nil {
		return err
	}
	if err := linkClaude(name); err != nil && !strings.Contains(err.Error(), "already exists as a folder") {
		return err
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // skip links and devices from an archive
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, _ := d.Info()
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}
