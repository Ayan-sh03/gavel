package main

import (
	"log"
	"net/http"
	"os"

	"bidding/internal/api"
)

func main() {
	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	handler := api.NewServer()
	log.Printf("api server listening on %s", addr)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("server shutdown: %v", err)
	}
}
