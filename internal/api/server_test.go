package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bidding/internal/api"
)

func TestHealthEndpoint(t *testing.T) {
	handler := api.NewServer()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("unexpected error calling health endpoint: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}

	var payload map[string]string
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if payload["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %q", payload["status"])
	}
}
