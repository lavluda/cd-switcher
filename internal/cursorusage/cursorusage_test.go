package cursorusage

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCurrent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"billingCycleEnd":"1700000000000","planUsage":{"totalPercentUsed":11.5}}`))
	}))
	defer srv.Close()
	old := periodURL
	periodURL = srv.URL
	t.Cleanup(func() { periodURL = old })
	body := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user_9"}`))
	readToken = func() (string, error) { return "aaa." + body + ".sig", nil }
	t.Cleanup(func() { readToken = accessToken })
	got, err := Current(t.Context())
	if err != nil || got.Percent != 11.5 || got.Resets.UnixMilli() != 1700000000000 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestUserID(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"auth0|user_123"}`))
	id, err := userID("aaa." + body + ".sig")
	if err != nil || id != "user_123" {
		t.Fatalf("id %q err %v", id, err)
	}
}

func TestEventsPage(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.Header.Get("Cookie") == "" || r.Method != http.MethodPost {
			t.Errorf("request %s cookie %q", r.Method, r.Header.Get("Cookie"))
		}
		ev := rawEvent{Timestamp: "1700000000000", Model: "grok-4.7"}
		ev.Tokens.Input = 10
		ev.Tokens.Output = 4
		ev.Tokens.Cents = 121
		b, _ := json.Marshal(map[string]any{"totalUsageEventsCount": 1, "usageEventsDisplay": []rawEvent{ev}})
		w.Write(b)
	}))
	defer srv.Close()
	old := eventsURL
	eventsURL = srv.URL
	t.Cleanup(func() { eventsURL = old })

	body := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user_9"}`))
	got, err := eventsWithToken(t.Context(), "aaa."+body+".sig", time.Unix(0, 0), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if pages != 1 || len(got) != 1 || got[0].Model != "grok-4.7" || got[0].Input != 10 || got[0].Cost != 1.21 {
		t.Fatalf("pages %d events %+v", pages, got)
	}
}
