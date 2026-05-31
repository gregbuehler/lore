package main

import (
	"os"

	"github.com/gregbuehler/lore/cmd/lore"
)

func main() {
	if err := lore.Execute(); err != nil {
		os.Exit(1)
	}
}
