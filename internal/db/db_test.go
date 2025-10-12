package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"bidding/internal/db"

	nat "github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestMigrationsCreateTables(t *testing.T) {
	ctx := context.Background()

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

	host, err := pgContainer.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}

	ports, err := pgContainer.Ports(ctx)
	if err != nil {
		t.Fatalf("failed to list container ports: %v", err)
	}

	var firstPort nat.Port
	for p := range ports {
		firstPort = p
		break
	}
	if firstPort.Port() == "" {
		t.Fatalf("no exposed ports found")
	}

	mapped, err := pgContainer.MappedPort(ctx, firstPort)
	if err != nil {
		t.Fatalf("failed to get container port: %v", err)
	}

	dsn := fmt.Sprintf("postgres://auction:auction@%s:%s/auction_test?sslmode=disable", host, mapped.Port())

	store, err := db.Open(ctx, dsn, "migrations")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	tables := []string{
		"users",
		"listings",
		"listing_images",
		"auctions",
		"bids",
		"watchlists",
		"payments",
		"payouts",
		"audit_log",
	}

	for _, table := range tables {
		if !tableExists(t, store.DB, table) {
			t.Fatalf("table %s not found", table)
		}
	}
}

func tableExists(t *testing.T, conn *sql.DB, name string) bool {
	t.Helper()

	var exists bool
	err := conn.QueryRow(`SELECT EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = current_schema()
          AND table_name = $1
    )`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("failed to query table %s: %v", name, err)
	}
	return exists
}
