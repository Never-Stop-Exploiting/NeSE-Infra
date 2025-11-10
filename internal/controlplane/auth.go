package controlplane

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

const runnerSecretHeader = "X-Runner-Secret"

func authMiddleware(next http.Handler, adminToken string, runnerSecret string) http.Handler {
	if strings.TrimSpace(adminToken) == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipAuthForRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		if allowRunnerSecret(r, runnerSecret) {
			next.ServeHTTP(w, r)
			return
		}
		if authorizeAdmin(r, adminToken) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized")
	})
}

func authorizeAdmin(r *http.Request, token string) bool {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" || !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return false
	}
	value := strings.TrimSpace(header[len("Bearer "):])
	if value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(token)) == 1
}

func allowRunnerSecret(r *http.Request, runnerSecret string) bool {
	if strings.TrimSpace(runnerSecret) == "" {
		return false
	}
	header := strings.TrimSpace(r.Header.Get(runnerSecretHeader))
	if header == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(header), []byte(runnerSecret)) != 1 {
		return false
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/runs/") {
		return true
	}
	if r.Method == http.MethodGet &&
		strings.HasPrefix(r.URL.Path, "/api/v1/exploits/") &&
		strings.HasSuffix(r.URL.Path, "/artifact") {
		return true
	}
	return false
}

func skipAuthForRequest(r *http.Request) bool {
	switch {
	case r.URL.Path == "/healthz":
		return true
	case r.URL.Path == "/api/v1/runners/register" && r.Method == http.MethodPost:
		return true
	case r.URL.Path == "/api/v1/runners/heartbeat" && r.Method == http.MethodPost:
		return true
	case r.URL.Path == "/api/v1/submitters/register" && r.Method == http.MethodPost:
		return true
	case r.URL.Path == "/api/v1/submitters/heartbeat" && r.Method == http.MethodPost:
		return true
	case strings.HasPrefix(r.URL.Path, "/api/v1/runs/") && r.Method == http.MethodPost:
		if strings.HasSuffix(r.URL.Path, "/start") || strings.HasSuffix(r.URL.Path, "/finish") {
			return true
		}
	case strings.HasPrefix(r.URL.Path, "/api/v1/flags/") && r.Method == http.MethodPost:
		if strings.HasSuffix(r.URL.Path, "/submit/start") || strings.HasSuffix(r.URL.Path, "/submit/finish") {
			return true
		}
	}
	return false
}
