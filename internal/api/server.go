package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"bidding/internal/admin"
	"bidding/internal/api/openapi"
	"bidding/internal/auctions"
	"bidding/internal/auth"
	"bidding/internal/bids"
	"bidding/internal/db"
	"bidding/internal/listings"
	"bidding/internal/payments"
	"bidding/internal/realtime"
	"bidding/internal/watchlist"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewServer wires the base HTTP routes for the API service.
func NewServer() http.Handler {
	// Setup database
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://auction:auction@localhost:5432/auction?sslmode=disable"
	}
	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		_, currentFile, _, _ := runtime.Caller(0)
		migrationsPath = filepath.Join(filepath.Dir(currentFile), "..", "db", "migrations")
	} else if !filepath.IsAbs(migrationsPath) {
		if abs, err := filepath.Abs(migrationsPath); err == nil {
			migrationsPath = abs
		}
	}
	if filepath.IsAbs(migrationsPath) {
		if wd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(wd, migrationsPath); err == nil {
				migrationsPath = rel
			}
		}
	}

	store, err := db.Open(context.Background(), dsn, migrationsPath)
	if err != nil {
		panic(err)
	}

	// Initialize handlers
	authHandler := auth.NewHandler(store)
	listingsHandler := listings.NewHandler(store)
	auctionsHandler := auctions.NewHandler(store)
	bidsHandler := bids.NewHandler(store)
	paymentsHandler := payments.NewHandler(store)
	watchlistHandler := watchlist.NewHandler(store)
	adminHandler := admin.NewHandler(store)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	specHandler := openapi.Handler()
	uiHandler := openapi.UI()

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// API documentation
	r.Get("/openapi.json", specHandler.ServeHTTP)
	r.Get("/docs", uiHandler.ServeHTTP)
	r.Get("/docs/", uiHandler.ServeHTTP)

	// Auth routes
	r.Post("/auth/register", authHandler.Register)
	r.Post("/auth/login", authHandler.Login)

	// Protected routes (require JWT)
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware)

		// User profile
		r.Get("/me", authHandler.GetMe)

		// Listings
		r.Post("/listings", listingsHandler.Create)
		r.Get("/listings", listingsHandler.List)
		r.Get("/listings/{id}", func(w http.ResponseWriter, req *http.Request) {
			listingsHandler.Get(w, req, chi.URLParam(req, "id"))
		})
		r.Patch("/listings/{id}", func(w http.ResponseWriter, r *http.Request) {
			listingsHandler.Update(w, r, chi.URLParam(r, "id"))
		})

		// Auctions
		r.Post("/auctions", auctionsHandler.Create)
		r.Get("/auctions", auctionsHandler.List)
		r.Get("/auctions/{id}", func(w http.ResponseWriter, r *http.Request) {
			auctionsHandler.Get(w, r, chi.URLParam(r, "id"))
		})
		r.Patch("/auctions/{id}", func(w http.ResponseWriter, r *http.Request) {
			auctionsHandler.Update(w, r, chi.URLParam(r, "id"))
		})
		r.Post("/auctions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
			auctionsHandler.Cancel(w, r, chi.URLParam(r, "id"))
		})

		// Bids
		r.Post("/auctions/{id}/bids", func(w http.ResponseWriter, r *http.Request) {
			bidsHandler.PlaceBid(w, r, chi.URLParam(r, "id"))
		})
		r.Get("/auctions/{id}/bids", func(w http.ResponseWriter, r *http.Request) {
			bidsHandler.GetBids(w, r, chi.URLParam(r, "id"))
		})

		// Payments
		r.Post("/auctions/{id}/pay", func(w http.ResponseWriter, r *http.Request) {
			paymentsHandler.Pay(w, r, chi.URLParam(r, "id"))
		})
		r.Get("/payments/{id}", func(w http.ResponseWriter, r *http.Request) {
			paymentsHandler.GetPayment(w, r, chi.URLParam(r, "id"))
		})

		// Watchlist
		r.Post("/auctions/{id}/watch", func(w http.ResponseWriter, r *http.Request) {
			watchlistHandler.AddToWatchlist(w, r, chi.URLParam(r, "id"))
		})
		r.Delete("/auctions/{id}/watch", func(w http.ResponseWriter, r *http.Request) {
			watchlistHandler.RemoveFromWatchlist(w, r, chi.URLParam(r, "id"))
		})
		r.Get("/me/watchlist", watchlistHandler.GetWatchlist)

		// Admin routes
		r.Get("/admin/auctions", adminHandler.ListAuctions)
		r.Get("/admin/users", adminHandler.ListUsers)
		r.Patch("/admin/users/{id}", func(w http.ResponseWriter, r *http.Request) {
			adminHandler.UpdateUser(w, r, chi.URLParam(r, "id"))
		})
		r.Get("/admin/stats", adminHandler.GetStats)
	})

	// SSE streaming (no auth required for demo)
	r.Get("/auctions/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		realtime.GetBroker().StreamHandler(w, r, chi.URLParam(r, "id"))
	})

	return r
}
