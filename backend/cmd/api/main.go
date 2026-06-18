package main

import (
	"log"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/server"
)

func main() {
	if err := database.Run(config.Load().DatabaseDSN, func(db *database.DB) error {
		return server.Run(server.New(db))
	}); err != nil {
		log.Fatal(err)
	}
}
