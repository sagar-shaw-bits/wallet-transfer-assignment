package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultDSNUsesEnvOverride(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example:secret@db.example.com:5432/testdb?sslmode=require")
	assert.Equal(t, "postgres://example:secret@db.example.com:5432/testdb?sslmode=require", DefaultDSN())
}

func TestDefaultDSNUsesLocalFallback(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	want := "postgres://postgres:postgres@localhost:5432/wallet_transfer?sslmode=disable"
	assert.Equal(t, want, DefaultDSN())
}
