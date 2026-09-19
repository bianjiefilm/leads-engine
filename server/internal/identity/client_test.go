package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: "test-identity-token", AppID: "leads-engine"}, srv
}

func TestResolveSessionContract(t *testing.T) {
	var gotPath, gotTokenHeader, gotAppHeader, gotAppIDBody string
	var gotBody map[string]string
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTokenHeader = r.Header.Get("X-PilotSeaView-Internal-Token")
		gotAppHeader = r.Header.Get("X-App-ID")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		gotAppIDBody = gotBody["app_id"]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authenticated": true,
			"app_id":        "leads-engine",
			"session":       map[string]any{"principal_id": "usr_123", "email": "alice@example.com"},
		})
	}))
	p, err := client.ResolveSession(context.Background(), "sess-token")
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if gotPath != "/internal/v1/identity/session/resolve" {
		t.Errorf("path = %s", gotPath)
	}
	if gotTokenHeader != "test-identity-token" || gotAppHeader != "leads-engine" {
		t.Errorf("headers = %s / %s", gotTokenHeader, gotAppHeader)
	}
	if gotBody["session_token"] != "sess-token" {
		t.Errorf("body session_token = %v", gotBody)
	}
	if gotAppIDBody != "" {
		// resolve carries only the session token per contract
		t.Errorf("unexpected app_id in resolve body: %s", gotAppIDBody)
	}
	if p.ID != "usr_123" || p.Email != "alice@example.com" {
		t.Errorf("principal = %+v", p)
	}
}

func TestResolveSessionFailClosed(t *testing.T) {
	t.Run("not authenticated", func(t *testing.T) {
		client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": false})
		}))
		if _, err := client.ResolveSession(context.Background(), "x"); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("want ErrUnauthenticated, got %v", err)
		}
	})
	t.Run("authenticated but no principal_id", func(t *testing.T) {
		client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authenticated": true,
				"session":       map[string]any{"email": "alice@example.com"},
			})
		}))
		if _, err := client.ResolveSession(context.Background(), "x"); !errors.Is(err, ErrNoPrincipal) {
			t.Fatalf("want ErrNoPrincipal, got %v", err)
		}
	})
	t.Run("identity rejects 401", func(t *testing.T) {
		client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		if _, err := client.ResolveSession(context.Background(), "x"); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("want ErrUnauthenticated, got %v", err)
		}
	})
	t.Run("identity unreachable is transport error, not auth failure", func(t *testing.T) {
		client := &Client{BaseURL: "http://127.0.0.1:1", Token: "t", AppID: "leads-engine"}
		_, err := client.ResolveSession(context.Background(), "x")
		if err == nil || errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("want transport error, got %v", err)
		}
	})
}

func TestRefreshCarriesAppID(t *testing.T) {
	var body map[string]string
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at2", "refresh_token": "rt2"})
	}))
	if _, err := client.Refresh(context.Background(), "rt1"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if body["app_id"] != "leads-engine" || body["refresh_token"] != "rt1" {
		t.Fatalf("refresh body = %v", body)
	}
}
