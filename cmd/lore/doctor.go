package lore

import (
	"fmt"
	"os"

	"github.com/gregbuehler/lore/internal/store"
	"github.com/spf13/cobra"
)

var doctorRepair bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the search index for damage",
	Long: `Verifies the SQLite full-text index behind 'lore query'.

FTS5 can be damaged in ways the obvious checks miss: plain MATCH, bm25(),
snippet() and FTS5's own integrity-check can all pass while the ranked
traversal used by 'ORDER BY rank' — which every 'lore query' relies on — still
fails with "database disk image is malformed (267)". Repopulating documents
(a reindex) does not clear the damaged segment; only an FTS5-level rebuild does.

  lore doctor            # verify, report
  lore doctor --repair   # verify, then rebuild the FTS index if unhealthy`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath := resolveVaultPath()

		dbPath, err := existingIndexPath(vaultPath)
		if err != nil {
			return err
		}

		// Legacy (pre per-vault) indexes carry no vault metadata, so open them
		// directly rather than asserting ownership. Skip the automatic repair on
		// open so the report reflects the index's real state and --repair can
		// report what it actually did.
		db, err := store.OpenNoRepair(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		fmt.Printf("Vault: %s\nIndex: %s\n", vaultPath, dbPath)

		docs, edges, err := db.Stats()
		if err != nil {
			return err
		}
		fmt.Printf("Documents: %d\nEdges: %d\n", docs, edges)
		if docs == 0 {
			fmt.Println("Warning: index is empty — run 'lore daemon reindex'")
		}

		if doctorRepair {
			repaired, err := db.RepairFTSIfNeeded()
			if err != nil {
				return fmt.Errorf("FTS: %w", err)
			}
			if repaired {
				fmt.Println("FTS: was unhealthy, rebuilt — OK")
			} else {
				fmt.Println("FTS: OK")
			}
			return nil
		}

		if err := db.VerifyFTS(); err != nil {
			return fmt.Errorf("FTS: %w\n  fix with: lore doctor --repair", err)
		}
		fmt.Println("FTS: OK (integrity-check and ranked probe passed)")
		return nil
	},
}

// existingIndexPath finds the index to inspect without creating one. It prefers
// the vault-scoped DB, falls back to the legacy shared path written by older
// daemons, and refuses to invent an empty DB that would report a clean bill of
// health for an index that is not actually in use.
func existingIndexPath(vaultPath string) (string, error) {
	scoped := store.DefaultPathForVault(vaultPath)
	if fileExists(scoped) {
		return scoped, nil
	}
	if legacy := store.DefaultPath(); fileExists(legacy) {
		return legacy, nil
	}
	return "", fmt.Errorf("no index found at %s. Run 'lore daemon start' to build it", scoped)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorRepair, "repair", false, "Rebuild the FTS index if verification fails")
}
