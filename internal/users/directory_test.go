package users

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"opk-whatsapp-ai/internal/pocketbase"
)

func directory(t *testing.T, stored []string) *Directory {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/collections/_superusers/auth-with-password" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "t"})
			return
		}
		items := []map[string]any{}
		for i, p := range stored {
			items = append(items, map[string]any{"id": string(rune('a' + i)), "name": "U", "role": "admin", "phone": p})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	t.Cleanup(srv.Close)

	pb := pocketbase.NewClient(srv.URL)
	if err := pb.Authenticate(context.Background(), "a@b.c", "x"); err != nil {
		t.Fatal(err)
	}
	return NewDirectory(pb, "233")
}

func TestFindByPhoneMatchesLocalFormat(t *testing.T) {
	u, err := directory(t, []string{"0241234567"}).FindByPhone(context.Background(), "233241234567")
	if err != nil || u == nil || u.ID != "a" {
		t.Fatalf("got %+v, %v", u, err)
	}
}

func TestFindByPhoneNoMatch(t *testing.T) {
	// Same suffix search result but a different full number must not match.
	u, err := directory(t, []string{"+447241234567"}).FindByPhone(context.Background(), "233241234567")
	if err != nil || u != nil {
		t.Fatalf("got %+v, %v", u, err)
	}
}

func TestFindByPhoneAmbiguous(t *testing.T) {
	_, err := directory(t, []string{"0241234567", "+233241234567"}).FindByPhone(context.Background(), "233241234567")
	if err != ErrAmbiguous {
		t.Fatalf("err = %v", err)
	}
}
