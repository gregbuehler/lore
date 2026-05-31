package lore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gregbuehler/lore/internal/config"
	"github.com/spf13/cobra"
)

var noteVault string
var noteTag string

var noteCmd = &cobra.Command{
	Use:   "note <text>",
	Short: "Append a quick note to today's daily log",
	Long: `Appends a bullet point to today's daily log file.

The daily log is located at <vault>/Daily Log/YYYY-MM/YYYY-MM-DD.md.
If the file does not exist it is created with standard frontmatter.

Examples:
  lore note "reviewed PRs with teammate"
  lore note "leader election RFC — pushback logged" --tag "#team/planning"
  lore note "s3 buckets created in staging" --vault ~/Documents/lore/jane`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text := strings.TrimSpace(args[0])
		if text == "" {
			return fmt.Errorf("note text must not be empty")
		}

		// Resolve vault path
		vaultPath := noteVault
		if vaultPath == "" {
			var err error
			vaultPath, err = config.FindVault()
			if err != nil {
				return fmt.Errorf("specify --vault or run from within a vault: %w", err)
			}
		} else {
			abs, err := filepath.Abs(vaultPath)
			if err != nil {
				return fmt.Errorf("resolving vault path: %w", err)
			}
			vaultPath = abs
		}

		// Validate vault exists
		if _, err := os.Stat(vaultPath); err != nil {
			return fmt.Errorf("vault path does not exist: %s", vaultPath)
		}

		// Compute today's date strings
		now := time.Now()
		dateStr := now.Format("2006-01-02") // YYYY-MM-DD
		monthStr := now.Format("2006-01")   // YYYY-MM

		// Build file path: <vault>/Daily Log/YYYY-MM/YYYY-MM-DD.md
		logDir := filepath.Join(vaultPath, "Daily Log", monthStr)
		logFile := filepath.Join(logDir, dateStr+".md")

		// Create directory if needed
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return fmt.Errorf("creating daily log directory: %w", err)
		}

		if err := createDailyLogIfMissing(logFile, dateStr); err != nil {
			return fmt.Errorf("creating daily log file: %w", err)
		}

		// Build bullet line
		var line string
		tag := strings.TrimSpace(noteTag)
		if tag != "" {
			line = fmt.Sprintf("- %s %s\n", tag, text)
		} else {
			line = fmt.Sprintf("- %s\n", text)
		}

		// Open file for appending
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("opening daily log file: %w", err)
		}
		defer f.Close()

		if _, err := f.WriteString(line); err != nil {
			return fmt.Errorf("appending note: %w", err)
		}

		fmt.Printf("noted: %s\n", logFile)
		return nil
	},
}

func init() {
	noteCmd.Flags().StringVar(&noteVault, "vault", "", "Path to vault (auto-detected if omitted)")
	noteCmd.Flags().StringVar(&noteTag, "tag", "", "Tag prefix to prepend to the note line (e.g. #admin/hiring)")
}

func createDailyLogIfMissing(logFile, dateStr string) error {
	scaffold := fmt.Sprintf("---\ntags:\n  - daily-log\n---\n# %s\n\n", dateStr)
	f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}

	if _, err := f.WriteString(scaffold); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
