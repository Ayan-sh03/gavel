package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bidding/internal/auth"
	"bidding/internal/db"

	nat "github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestRegister(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	handler := auth.NewHandler(store)

	reqBody := map[string]string{
		"email":        "test@example.com",
		"password":     "password123",
		"display_name": "Test User",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Register(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["id"] == nil || resp["email"] == nil {
		t.Fatalf("expected id and email in response, got %v", resp)
	}
}

func setupTestDB(t *testing.T, ctx context.Context) *db.Store {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "auction_test",
			"POSTGRES_USER":     "auction",
			"POSTGRES_PASSWORD": "auction",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp"),
	}

	pgContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = pgContainer.Terminate(ctx)
	})

	host, _ := pgContainer.Host(ctx)
	ports, _ := pgContainer.Ports(ctx)

	var firstPort nat.Port
	for p := range ports {
		firstPort = p
		break
	}

	mapped, _ := pgContainer.MappedPort(ctx, firstPort)
	dsn := "postgres://auction:auction@" + host + ":" + mapped.Port() + "/auction_test?sslmode=disable"

	store, err := db.Open(ctx, dsn, "../../internal/db/migrations")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	return store
}

func TestLogin(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	handler := auth.NewHandler(store)

	registerBody := map[string]string{
		"email":        "login@example.com",
		"password":     "password123",
		"display_name": "Login User",
	}
	body, _ := json.Marshal(registerBody)
	regReq := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	handler.Register(regRec, regReq)

	loginBody := map[string]string{
		"email":    "login@example.com",
		"password": "password123",
	}
	body, _ = json.Marshal(loginBody)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Login(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["token"] == nil {
		t.Fatalf("expected token in response, got %v", resp)
	}
}

func TestAuthMiddleware(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	handler := auth.NewHandler(store)

	registerBody := map[string]string{
		"email":        "middleware@example.com",
		"password":     "password123",
		"display_name": "Middleware User",
	}
	body, _ := json.Marshal(registerBody)
	regReq := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	handler.Register(regRec, regReq)

	loginBody := map[string]string{
		"email":    "middleware@example.com",
		"password": "password123",
	}
	body, _ = json.Marshal(loginBody)
	loginReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.Login(loginRec, loginReq)

	var loginResp map[string]interface{}
	json.NewDecoder(loginRec.Body).Decode(&loginResp)
	token := loginResp["token"].(string)

	protectedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := auth.GetUserID(r.Context())
		if userID == "" {
			t.Fatal("expected user_id in context")
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"user_id": userID})
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	auth.Middleware(protectedHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetMe(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t, ctx)
	defer store.Close()

	handler := auth.NewHandler(store)

	registerBody := map[string]string{
		"email":        "me@example.com",
		"password":     "password123",
		"display_name": "Me User",
	}
	body, _ := json.Marshal(registerBody)
	regReq := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(body))
	regReq.Header.Set("Content-Type", "application/json")
	regRec := httptest.NewRecorder()
	handler.Register(regRec, regReq)

	var regResp map[string]interface{}
	json.NewDecoder(regRec.Body).Decode(&regResp)
	userID := regResp["id"].(string)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.GetUserIDKey(), userID))
	rec := httptest.NewRecorder()

	handler.GetMe(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["email"] != "me@example.com" {
		t.Fatalf("expected email me@example.com, got %v", resp["email"])
	}
}
