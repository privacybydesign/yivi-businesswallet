package config

import "os"

const defaultDatabaseURL = "postgres://postgres:postgres@localhost:5432/yivi_business_wallet?sslmode=disable"

const envDatabaseURL = "DATABASE_URL"

type Config struct {
	DatabaseDSN string
}

func Load() Config {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		dsn = defaultDatabaseURL
	}
	return Config{DatabaseDSN: dsn}
}
