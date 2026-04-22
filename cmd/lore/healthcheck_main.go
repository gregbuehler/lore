//go:build ignore

package main

import (
	"fmt"
	"github.com/gbuehler/lore/internal/daemon"
)

func main() {
	client, err := daemon.Connect()
	if err != nil {
		fmt.Println("connect err:", err)
		return
	}
	defer client.Close()

	resp, err := client.Send(&daemon.Request{Type: "health"})
	if err != nil {
		fmt.Println("send err:", err)
		return
	}
	
	broken := 0
	orphan := 0
	stale := 0
	for _, r := range resp.Results {
		switch r.EdgeType {
		case "broken_link":
			broken++
		case "orphan":
			orphan++
		case "stale":
			stale++
		}
	}
	fmt.Printf("Broken links: %d\nOrphans: %d\nStale: %d\nTotal: %d\n", broken, orphan, stale, len(resp.Results))
	
	if broken > 0 {
		fmt.Println("\n--- Broken links ---")
		for _, r := range resp.Results {
			if r.EdgeType == "broken_link" {
				fmt.Println(" ", r.Title)
			}
		}
	}
}
