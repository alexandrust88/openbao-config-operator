package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepositoryRoutes(t *testing.T) {
	for _, test := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/repo.git/info/refs", true}, {"POST", "/repo.git/git-upload-pack", true},
		{"POST", "/repo.git/git-receive-pack", true}, {"GET", "/repo.git/config", false},
		{"GET", "/other.git/info/refs", false}, {"PUT", "/repo.git/git-receive-pack", false},
	} {
		called := false
		handler := repositoryHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(test.method, test.path, nil))
		if called != test.allowed {
			t.Fatalf("route %s %s allowed=%v", test.method, test.path, called)
		}
		if !called && result.Code != http.StatusNotFound {
			t.Fatalf("unexpected rejected status %d", result.Code)
		}
	}
}
