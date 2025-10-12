package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"bidding/internal/db"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: go run cmd/createadmin/main.go <email> <password> <display_name>")
		fmt.Println("Example: go run cmd/createadmin/main.go admin@example.com password123 \"Admin User\"")
		os.Exit(1)
	}

	email := os.Args[1]
	password := os.Args[2]
	displayName := os.Args[3]

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

	// Check if user already exists
	var existingID string
	err = store.DB.QueryRow(`SELECT id FROM users WHERE email = $1`, email).Scan(&existingID)
	if err == nil {
		log.Fatalf("user with email %s already exists (id: %s)", email, existingID)
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("failed to hash password: %v", err)
	}

	// Create admin user
	userID := uuid.New().String()
	_, err = store.DB.Exec(
		`INSERT INTO users (id, email, password_hash, display_name, role, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())`,
		userID, email, string(hash), displayName, "admin", "active",
	)
	if err != nil {
		log.Fatalf("failed to create admin user: %v", err)
	}

	fmt.Printf("✓ Admin user created successfully!\n")
	fmt.Printf("  ID: %s\n", userID)
	fmt.Printf("  Email: %s\n", email)
	fmt.Printf("  Display Name: %s\n", displayName)
	fmt.Printf("  Role: admin\n")
	fmt.Printf("\nYou can now login with these credentials:\n")
	fmt.Printf("  curl -X POST http://localhost:8080/auth/login \\\n")
	fmt.Printf("    -H \"Content-Type: application/json\" \\\n")
	fmt.Printf("    -d '{\"email\":\"%s\",\"password\":\"%s\"}'\n", email, password)
}
