package modules

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSettingsUIAuthenticationCSRFAndKeyPrivacy(t *testing.T) {
	p := paths{config: t.TempDir()}
	c := defaults()
	c.APIKey = "private-key"
	saveSettings(p, c)
	wakes := 0
	handler := settingsHandler(p, "session-secret", "127.0.0.1:12345", func() { wakes++ })
	request := httptest.NewRequest("GET", "http://127.0.0.1:12345/settings", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatal("unauthenticated settings exposed")
	}
	request = httptest.NewRequest("GET", "http://127.0.0.1:12345/settings", nil)
	request.AddCookie(&http.Cookie{Name: "tracker-session", Value: "session-secret"})
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 200 || strings.Contains(w.Body.String(), c.APIKey) || strings.Contains(w.Body.String(), builtInApplicationID()) {
		t.Fatal("key displayed")
	}
	form := url.Values{"csrf": {"session-secret"}, "appid": {"123"}, "language": {"en-US"}, "days": {"60"}, "fit": {"raw"}, "enabled": {"on"}}
	post := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:12345/settings", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.AddCookie(&http.Cookie{Name: "tracker-session", Value: "session-secret"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if post("http://untrusted.test").Code != 403 || wakes != 0 {
		t.Fatal("cross-site request accepted")
	}
	if post("http://127.0.0.1:12345").Code != 200 || wakes != 1 {
		t.Fatal("valid settings rejected")
	}
	form.Set("days", "0")
	post("http://127.0.0.1:12345")
	if wakes != 1 {
		t.Fatal("invalid settings woke service")
	}
	saved, _ := loadSettings(p)
	if saved.APIKey != c.APIKey || saved.ApplicationID != "123" {
		t.Fatal("key not retained")
	}
	form.Set("days", "60")
	form.Set("appid", "")
	form.Set("startup_notification", "on")
	if w := post("http://127.0.0.1:12345"); w.Code != 200 || strings.Contains(w.Body.String(), builtInApplicationID()) {
		t.Fatal("blank custom ID rejected or exposed default")
	}
	saved, err := loadSettings(p)
	if err != nil || saved.ApplicationID != "" || !saved.StartupNotification || saved.discordApplicationID() == "" {
		t.Fatal("default ID or notification preference", saved, err)
	}
	form.Del("startup_notification")
	post("http://127.0.0.1:12345")
	saved, err = loadSettings(p)
	if err != nil || saved.StartupNotification {
		t.Fatal("notification toggle not saved", err)
	}
}
