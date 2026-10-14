package agent

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkbenchEmbeddedAssets(t *testing.T) {
	s := testStore(t)
	h := NewServer(&Runtime{LoadIndex(), Demo{}}, s, "reader-local-key-12345", "approver-local-key-67890").Handler()
	for _, tc := range []struct{ path, contentType, marker string }{
		{"/", "text/html", "恢复已有运行"},
		{"/app.js", "text/javascript", "textContent"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(w.Body.String(), tc.marker) {
			t.Fatalf("%s: unexpected asset response %d", tc.path, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatal("same-origin script blocked")
		}
	}
}
