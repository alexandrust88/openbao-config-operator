// The disposable lab serves one credential-free repository through Git's smart HTTP backend.
package main

import (
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	for _, args := range [][]string{
		{"init", "--bare", "--initial-branch=main", "/git/repo.git"},
		{"--git-dir=/git/repo.git", "config", "http.receivepack", "true"},
	} {
		if err := exec.Command("git", args...).Run(); err != nil {
			log.Fatal("Git repository setup failed")
		}
	}
	path, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		log.Fatal("Git backend not available")
	}
	backend := &cgi.Handler{
		Path:       filepath.Join(strings.TrimSpace(string(path)), "git-http-backend"),
		Env:        []string{"GIT_PROJECT_ROOT=/git", "GIT_HTTP_EXPORT_ALL=1"},
		InheritEnv: []string{"PATH"},
	}
	handler := repositoryHandler(backend)
	server := &http.Server{
		Addr: ":8080", Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 120 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 30 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		os.Exit(1)
	}
}

func repositoryHandler(backend http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		valid := r.Method == "GET" && r.URL.Path == "/repo.git/info/refs" ||
			r.Method == "POST" && (r.URL.Path == "/repo.git/git-upload-pack" || r.URL.Path == "/repo.git/git-receive-pack")
		if !valid {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
		backend.ServeHTTP(w, r)
	})
}
