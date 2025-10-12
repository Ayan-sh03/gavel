package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"bidding/internal/db"
	"bidding/internal/jobs"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://auction:auction@localhost:5432/auction?sslmode=disable"
	}

	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = "internal/db/migrations"
	}

	ctx := context.Background()
	store, err := db.Open(ctx, dsn, migrationsPath)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer store.Close()

	log.Println("Starting auction finalization worker...")

	finalizer := jobs.NewAuctionFinalizer(store)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nShutting down worker...")
		cancel()
	}()

	if err := finalizer.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("worker error: %v", err)
	}

	log.Println("Worker stopped")
}
