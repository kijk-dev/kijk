package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestGuard(t *testing.T) {
	web := fstest.MapFS{"index.html": {Data: []byte("hi")}}
	srv := newServer("secret", 7717, web, newHub(), &roots{}, newUpdater("0.2.0", "", io.Discard))
	do := func(host, target string, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		r.Host = host
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: cookie})
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}

	for _, host := range []string{"evil.example:7717", "127.0.0.1:9999", "127.0.0.1"} {
		if c := do(host, "/", "secret").Code; c != http.StatusForbidden {
			t.Errorf("host %q: code %d", host, c)
		}
	}
	if c := do("127.0.0.1:7717", "/", "").Code; c != http.StatusForbidden {
		t.Errorf("no cookie: code %d", c)
	}
	if c := do("127.0.0.1:7717", "/", "wrong").Code; c != http.StatusForbidden {
		t.Errorf("wrong cookie: code %d", c)
	}
	if c := do("127.0.0.1:7717", "/?token=wrong", "").Code; c != http.StatusForbidden {
		t.Errorf("wrong token: code %d", c)
	}

	w := do("localhost:7717", "/?token=secret", "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("token exchange: %d %q", w.Code, w.Header().Get("Location"))
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != "kijk-7717" || c[0].Value != "secret" || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie = %+v", c)
	}
	if w := do("127.0.0.1:7717", "/", "secret"); w.Code != http.StatusOK || w.Body.String() != "hi" {
		t.Fatalf("with cookie: %d %q", w.Code, w.Body.String())
	} else if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}

	post := func(origin string) (int, string) {
		r := httptest.NewRequest("POST", "/api/commit", strings.NewReader(`{"root":"/nope"}`))
		r.Host = "127.0.0.1:7717"
		r.Header.Set("Origin", origin)
		r.AddCookie(&http.Cookie{Name: cookieName(7717), Value: "secret"})
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if c, body := post("http://evil.example"); c != http.StatusForbidden || !strings.Contains(body, "origin") {
		t.Errorf("foreign origin: %d %q", c, body)
	}
	if _, body := post("http://127.0.0.1:7717"); !strings.Contains(body, "no running agent") {
		t.Errorf("own origin should pass the guard: %q", body)
	}
}
