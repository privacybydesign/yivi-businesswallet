package config

import (
	"os"
	"strings"
)

const (
	envDatabaseURL = "DATABASE_URL"
	envLogLevel    = "LOG_LEVEL"
	envLogFormat   = "LOG_FORMAT"
	envLogSource   = "LOG_SOURCE"

	defaultDatabaseURL = "postgres://postgres:postgres@localhost:5432/yivi_business_wallet?sslmode=disable"
	defaultLogLevel    = "info"
	defaultLogFormat   = "text"
	defaultLogSource   = "true"
)

type Config struct {
	DatabaseDSN string
	LogLevel    string
	LogFormat   string
	LogSource   bool
}

func Load() (Config, error) {
	return Config{
		DatabaseDSN: envOrDefault(envDatabaseURL, defaultDatabaseURL),
		LogLevel:    envOrDefault(envLogLevel, defaultLogLevel),
		LogFormat:   envOrDefault(envLogFormat, defaultLogFormat),
		LogSource:   strings.EqualFold(envOrDefault(envLogSource, defaultLogSource), "true"),
	}, nil
}

func envOrDefault(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
