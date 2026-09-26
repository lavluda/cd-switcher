package claude

import "testing"

func TestDataDirFromArgs(t *testing.T) {
	dir, ok := DataDirFromArgs([]string{"Claude", "--user-data-dir=/tmp/acct"})
	if !ok || dir != "/tmp/acct" {
		t.Fatalf("got %q %v", dir, ok)
	}
	dir, ok = DataDirFromArgs([]string{"Claude", "--user-data-dir", "/tmp/other"})
	if !ok || dir != "/tmp/other" {
		t.Fatalf("got %q %v", dir, ok)
	}
	if _, ok := DataDirFromArgs([]string{"Claude"}); ok {
		t.Fatal("expected no data dir")
	}
}
