package main

import (
	"log"
	"os"

	"wallet-transfer-assignment/internal/db"
	"wallet-transfer-assignment/internal/handler"
	"wallet-transfer-assignment/internal/repository"
	"wallet-transfer-assignment/internal/service"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = db.DefaultDSN()
	}

	conn, err := db.Open(dsn)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := db.InitSchema(conn); err != nil {
		log.Fatalf("failed to initialize schema: %v", err)
	}

	repo := repository.NewRepository(conn)
	transferService := service.NewService(repo)
	r := handler.NewRouter(transferService)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	if err := r.Run("0.0.0.0:" + port); err != nil {
		log.Fatalf("server startup failed: %v", err)
	}
}
