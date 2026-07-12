package readiness

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStateDefaultsToNotReady(t *testing.T) {
	var s State
	if s.Ready() {
		t.Error("new State should not be ready")
	}
}

func TestStateSetReady(t *testing.T) {
	var s State

	s.SetReady(true)
	if !s.Ready() {
		t.Error("expected ready after SetReady(true)")
	}

	s.SetReady(false)
	if s.Ready() {
		t.Error("expected not ready after SetReady(false)")
	}
}

func TestHandlerReadyz(t *testing.T) {
	state := &State{}
	handler := Handler(state)

	probe := func() int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ReadyzPath, nil))
		return rec.Code
	}

	if code := probe(); code != http.StatusServiceUnavailable {
		t.Errorf("not-ready probe returned %d, want %d", code, http.StatusServiceUnavailable)
	}

	state.SetReady(true)
	if code := probe(); code != http.StatusOK {
		t.Errorf("ready probe returned %d, want %d", code, http.StatusOK)
	}

	state.SetReady(false)
	if code := probe(); code != http.StatusServiceUnavailable {
		t.Errorf("probe after connection drop returned %d, want %d", code, http.StatusServiceUnavailable)
	}
}
