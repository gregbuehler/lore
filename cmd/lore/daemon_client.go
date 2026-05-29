package lore

import "github.com/gbuehler/lore/internal/daemon"

func connectDaemonForCurrentVault() (*daemon.Client, error) {
	vaultPath := resolveVaultPath()
	if vaultPath != "" {
		return daemon.EnsureDaemon(vaultPath)
	}
	return daemon.Connect()
}
