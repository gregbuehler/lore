package main

import (
	"os"

	"github.com/gbuehler/lore/cmd/lore"
)

func main() {
	if err := lore.Execute(); err != nil {
		os.Exit(1)
	}
}
