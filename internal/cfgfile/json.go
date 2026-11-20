// Package cfgfile edits other tools' config files safely: it parses before it
// writes and refuses a file it cannot parse, keeps key order and every key it
// was not asked to change, backs up the first write it makes to each file,
// writes atomically, and keeps a dotfiles symlink pointing at the real file.
package cfgfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lavluda/cd-switcher/internal/fslock"
)

// BackupDir receives a copy of each file the first time it is changed. Empty
// turns backups off (tests).
var BackupDir string

// Object is a JSON object that remembers its key order.
type Object struct {
	keys []string
	vals map[string]json.RawMessage
}

// NewObject returns an empty object.
func NewObject() *Object { return &Object{vals: map[string]json.RawMessage{}} }

// ParseObject reads a JSON object, keeping key order.
func ParseObject(data []byte) (*Object, error) {
	o := NewObject()
	if err := json.Unmarshal(data, o); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *Object) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("not a JSON object")
	}
	o.keys, o.vals = nil, map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		if _, dup := o.vals[key]; !dup {
			o.keys = append(o.keys, key)
		}
		o.vals[key] = v
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after JSON object")
	}
	return nil
}

func (o *Object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Keys lists the keys in file order.
func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

// Has reports whether key is present.
func (o *Object) Has(key string) bool { _, ok := o.vals[key]; return ok }

// Raw returns key's value as written.
func (o *Object) Raw(key string) (json.RawMessage, bool) { v, ok := o.vals[key]; return v, ok }

// Get decodes key's value into v; a missing key leaves v alone.
func (o *Object) Get(key string, v any) error {
	raw, ok := o.vals[key]
	if !ok {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// Set stores v under key, keeping the key's place if it already exists.
func (o *Object) Set(key string, v any) error {
	raw, ok := v.(json.RawMessage)
	if !ok {
		b, err := marshal(v)
		if err != nil {
			return err
		}
		raw = b
	}
	if _, exists := o.vals[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
	return nil
}

// Delete removes key and reports whether it was there.
func (o *Object) Delete(key string) bool {
	if _, ok := o.vals[key]; !ok {
		return false
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
	return true
}

// Child returns the object under key, or an empty one when it is missing.
// A value that is not an object is an error rather than silently replaced.
func (o *Object) Child(key string) (*Object, error) {
	raw, ok := o.vals[key]
	if !ok || string(raw) == "null" {
		return NewObject(), nil
	}
	c, err := ParseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%q is not a JSON object", key)
	}
	return c, nil
}

// Len is the number of keys.
func (o *Object) Len() int { return len(o.keys) }

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// ReadJSON returns the object in path; a missing file is an empty object.
func ReadJSON(path string) (*Object, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return NewObject(), nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return NewObject(), nil
	}
	o, err := ParseObject(b)
	if err != nil {
		return nil, fmt.Errorf("%s is not plain JSON (%v); left unchanged", path, err)
	}
	return o, nil
}

// UpdateJSON applies fn to the object in path and writes the result back when
// fn reports a change. lockStale, when non-zero, takes the Claude Code style
// "<path>.lock" directory lock for the duration. private writes the file as
// owner-only, for files that end up holding a key.
func UpdateJSON(path string, lockStale time.Duration, private bool, fn func(doc *Object) (bool, error)) error {
	path = resolve(path)
	if lockStale > 0 {
		l, err := fslock.Acquire(path+".lock", lockStale, 8*time.Second)
		if err != nil {
			return err
		}
		defer l.Release()
	}
	doc, err := ReadJSON(path)
	if err != nil {
		return err
	}
	changed, err := fn(doc)
	if err != nil || !changed {
		return err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(path, append(out, '\n'), private)
}

// WriteFile replaces path atomically. It keeps the existing file's mode, or
// uses 0600 for private and new files that hold secrets, 0644 otherwise. The
// first write to each path is backed up to BackupDir.
func WriteFile(path string, data []byte, private bool) error {
	path = resolve(path)
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
		if err := backupOnce(path); err != nil {
			return fmt.Errorf("back up %s: %w", path, err)
		}
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if private {
		mode &= 0o600
	}
	tmp := path + ".cdswitcher-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // WriteFile's mode is masked by umask
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// resolve follows a symlink so a write replaces the real file, not the link.
func resolve(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

func backupOnce(path string) error {
	if BackupDir == "" {
		return nil
	}
	name := strings.NewReplacer(string(filepath.Separator), "_", ":", "_").Replace(strings.TrimPrefix(path, string(filepath.Separator)))
	dst := filepath.Join(BackupDir, name+".orig")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(BackupDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}
