package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	maxOpenConns    = 25
	maxIdleConns    = 5
	connMaxLifetime = 5 * time.Minute
	pingTimeout     = 5 * time.Second
)

type DB struct {
	gorm *gorm.DB
	sql  *sql.DB
}

func Run(dsn string, fn func(*DB) error) error {
	db, err := Open(dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()
	return fn(db)
}

func Open(dsn string) (*DB, error) {
	g, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		return nil, fmt.Errorf("open gorm postgres: %w", err)
	}

	s, err := g.DB()
	if err != nil {
		return nil, fmt.Errorf("access underlying sql.DB: %w", err)
	}
	s.SetMaxOpenConns(maxOpenConns)
	s.SetMaxIdleConns(maxIdleConns)
	s.SetConnMaxLifetime(connMaxLifetime)

	db := &DB{gorm: g, sql: s}
	if err := db.Ping(context.Background()); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *DB) Gorm() *gorm.DB { return db.gorm }

func (db *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.sql.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

func (db *DB) Close() error {
	if err := db.sql.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}
