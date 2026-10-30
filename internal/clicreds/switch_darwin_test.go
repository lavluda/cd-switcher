//go:build darwin

package clicreds

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeKeychain swaps the Keychain for a map and home for a temp dir.
func fakeKeychain(t *testing.T) (items map[string]string, h string) {
	t.Helper()
	items = map[string]string{}
	h = t.TempDir()
	if err := os.MkdirAll(filepath.Join(h, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldR, oldW, oldD, oldH := read, write, remove, home
	t.Cleanup(func() { read, write, remove, home = oldR, oldW, oldD, oldH })
	read = func(s, _ string) (string, bool, error) { v, ok := items[s]; return v, ok, nil }
	write = func(s, _, d string) error { items[s] = d; return nil }
	remove = func(s, _ string) error { delete(items, s); return nil }
	home = func() (string, error) { return h, nil }
	return items, h
}

func profileDoc(t *testing.T, h string) map[string]json.RawMessage {
	t.Helper()
	doc, err := readJSONFile(filepath.Join(h, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// compact makes indented JSON comparable as text.
func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

func TestSwitchMovesAccountKeysOnly(t *testing.T) {
	items, h := fakeKeychain(t)
	state := t.TempDir()
	cfg := filepath.Join(h, ".claude.json")

	// Account A is signed in to the CLI, with a machine-wide MCP grant.
	items[liveService] = `{"claudeAiOauth":{"refreshToken":"A"},"organizationUuid":"orgA","mcpOAuth":{"srv":"M1"}}`
	os.WriteFile(cfg, []byte(`{"oauthAccount":{"emailAddress":"a@x"},"projects":{"p":1},"modelAccessCache":{"x":1}}`), 0o600)
	if err := Save("a"); err != nil {
		t.Fatal(err)
	}

	// Account B has never signed in: switching to it signs the CLI out but
	// keeps the MCP grant and unrelated settings.
	if found, err := Restore(state, "b"); err != nil || found {
		t.Fatalf("restore b: found=%v err=%v", found, err)
	}
	if got := items[liveService]; got != `{"mcpOAuth":{"srv":"M1"}}` {
		t.Fatalf("live after signed-out restore = %s", got)
	}
	doc := profileDoc(t, h)
	if _, ok := doc["oauthAccount"]; ok {
		t.Fatal("oauthAccount kept for a signed-out account")
	}
	if _, ok := doc["modelAccessCache"]; ok {
		t.Fatal("account cache kept")
	}
	if compact(doc["projects"]) != `{"p":1}` {
		t.Fatalf("unrelated key changed: %s", doc["projects"])
	}

	// Meanwhile a new MCP grant lands; switching back to A restores A's login
	// and profile without losing it.
	items[liveService] = `{"mcpOAuth":{"srv":"M2"}}`
	if found, err := Restore(state, "a"); err != nil || !found {
		t.Fatalf("restore a: found=%v err=%v", found, err)
	}
	live, _ := parseObject(items[liveService])
	if string(live["mcpOAuth"]) != `{"srv":"M2"}` || string(live["claudeAiOauth"]) != `{"refreshToken":"A"}` || string(live["organizationUuid"]) != `"orgA"` {
		t.Fatalf("live after restore a = %s", items[liveService])
	}
	if got := compact(profileDoc(t, h)["oauthAccount"]); got != `{"emailAddress":"a@x"}` {
		t.Fatalf("oauthAccount = %s", got)
	}
	if _, err := os.Stat(filepath.Join(state, journalName)); !os.IsNotExist(err) {
		t.Fatal("journal left after a finished switch")
	}
	for _, l := range []string{".claude/.oauth_refresh.lock", ".claude.lock", ".claude/.storage-write.lock", ".claude.json.lock"} {
		if _, err := os.Stat(filepath.Join(h, l)); !os.IsNotExist(err) {
			t.Fatalf("lock %s left behind", l)
		}
	}
}

func TestSaveWithoutLoginClearsStored(t *testing.T) {
	items, _ := fakeKeychain(t)
	items[storedService("a")] = `{"claudeAiOauth":{"refreshToken":"old"}}`
	items[liveService] = `{"mcpOAuth":{}}`
	if err := Save("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := items[storedService("a")]; ok {
		t.Fatal("stale login kept for an account the CLI is signed out of")
	}
}

func TestRecoverFinishesInterruptedSwitch(t *testing.T) {
	items, h := fakeKeychain(t)
	state := t.TempDir()
	items[storedService("a")] = `{"claudeAiOauth":{"refreshToken":"A"},"` + profileKey + `":{"emailAddress":"a@x"}}`
	// Crash after the journal was written, before anything else.
	os.WriteFile(filepath.Join(state, journalName), []byte("a"), 0o600)
	if err := Recover(state); err != nil {
		t.Fatal(err)
	}
	if items[liveService] != `{"claudeAiOauth":{"refreshToken":"A"}}` {
		t.Fatalf("live = %s", items[liveService])
	}
	if got := compact(profileDoc(t, h)["oauthAccount"]); got != `{"emailAddress":"a@x"}` {
		t.Fatalf("oauthAccount = %s", got)
	}
	if err := Recover(state); err != nil { // no journal: nothing to do
		t.Fatal(err)
	}
}

func TestProfileThatIsNotJSONIsLeftAlone(t *testing.T) {
	_, h := fakeKeychain(t)
	cfg := filepath.Join(h, ".claude.json")
	os.WriteFile(cfg, []byte("not json"), 0o600)
	if err := writeProfile(cfg, nil); err == nil {
		t.Fatal("expected an error for a non-JSON profile")
	}
	if b, _ := os.ReadFile(cfg); string(b) != "not json" {
		t.Fatalf("file changed: %q", b)
	}
}
