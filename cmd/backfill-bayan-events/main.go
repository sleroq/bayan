package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/sleroq/bayan/internal/storage"
)

func main() {
	databasePath := flag.String("db", "bayan.db", "path to the SQLite database")
	flag.Parse()

	store, err := storage.New(*databasePath)
	if err != nil {
		log.Fatal(fmt.Errorf("opening database: %w", err))
	}

	inserted, err := store.BackfillBayanEvents()
	if err != nil {
		log.Fatal(fmt.Errorf("backfilling bayan events: %w", err))
	}

	fmt.Printf("Backfilled %d bayan events.\n", inserted)
}
