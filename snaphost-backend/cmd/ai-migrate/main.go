package main

import (
	"log"
	"os"

	"snaphost/internal/ai/db"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	if err := db.Migrate(databaseURL); err != nil {
		log.Fatalf("migrations failed: %v", err)
	}
}
