package pocketbase

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReauthenticatesOnExpiredToken checks that a 401 triggers one re-login
// and the request is retried with the fresh token.
func TestReauthenticatesOnExpiredToken(t *testing.T) {
	logins := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/collections/_superusers/auth-with-password":
			logins++
			token := "old"
			if logins > 1 {
				token = "new"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": token})

		case "/api/collections/customers/records":
			if r.Header.Get("Authorization") != "Bearer new" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if got := r.URL.Query().Get("filter"); got != "name ~ 'Kofi'" {
				t.Errorf("filter = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page": 1, "perPage": 1, "totalItems": 7, "totalPages": 7,
				"items": []map[string]any{{"id": "abc"}},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	ctx := context.Background()

	if err := client.Authenticate(ctx, "admin@example.com", "secret"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	count, err := client.CountRecords(ctx, "customers", "name ~ 'Kofi'")
	if err != nil {
		t.Fatalf("CountRecords: %v", err)
	}
	if count != 7 {
		t.Errorf("count = %d, want 7", count)
	}
	if logins != 2 {
		t.Errorf("logins = %d, want 2", logins)
	}
}
