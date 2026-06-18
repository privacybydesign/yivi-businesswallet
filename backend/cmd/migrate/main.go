package main

import (
	"log"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

func main() {
	if err := database.Run(config.Load().DatabaseDSN, (*database.DB).Migrate); err != nil {
		log.Fatal(err)
	}
	log.Println("migrations applied")
}
