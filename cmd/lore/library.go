package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

var libraryCmd = &cobra.Command{
	Use:   "library",
	Short: "Library administration commands",
	Long:  `Commands for creating and maintaining shared libraries.`,
}

var libraryInitCmd = &cobra.Command{
	Use:   "init <path>",
	Short: "Initialize a new shared library",
	Long: `Creates a new library directory structure. You still need to create the
GHE repo and push this content to it.

Scaffolds:
  library.yaml          Schema, tone rules, publishing model
  CLAUDE.md             Agent instructions for this library
  Wiki/index.md         Navigation index
  sources/              Raw source directory
  sources/incoming/     Contributor drop zone
  log.md                Activity log`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		libPath := args[0]
		abs, err := filepath.Abs(libPath)
		if err != nil {
			return err
		}

		// Create directories
		dirs := []string{
			filepath.Join(abs, "Wiki"),
			filepath.Join(abs, "skills"),
			filepath.Join(abs, "sources", "incoming"),
		}
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return fmt.Errorf("creating %s: %w", d, err)
			}
		}

		today := time.Now().Format("2006-01-02")

		// library.yaml
		libraryYaml := fmt.Sprintf(`# Library configuration
name: "%s"
description: ""

publishing: pr-required   # pr-required | direct-push

tone:
  voice: third-person
  prohibited:
    - personal characterizations of individuals' motivations or receptiveness
    - strategic framing
    - speculative assessments of org dynamics
  required:
    - factual sourcing (link to PR, ticket, Slack thread, or runbook)

classification:
  max_level: UNCLASSIFIED
  marking_required: true

default_ttl:
  service: 90d
  runbook: 30d
  environment-config: 14d
  person: 60d

# Skills — procedural knowledge for answering domain questions.
# Each skill is a markdown file in skills/ that encodes retrieval paths.
#
# skills:
#   - name: skill-name
#     file: skills/skill-name.md
#     description: What this skill helps you answer

# Source repositories tracked by 'lore watch'.
# Uncomment and configure to enable repo-driven curation.
#
# sources:
#   - repo: git.example.com/org/repo
#     local: ~/src/git.example.com/org/repo
#     watch:
#       - path: "directory/{entity}/**"
#         maps_to: entity_type
`, filepath.Base(abs))
		if err := os.WriteFile(filepath.Join(abs, "library.yaml"), []byte(libraryYaml), 0o644); err != nil {
			return err
		}

		// CLAUDE.md
		claudeMd := fmt.Sprintf(`# %s Library

This is a lore-managed shared library. Initialized %s.

## Structure

- Wiki/              Entity pages (the knowledge layer)
- Wiki/index.md      Navigation index (read this first)
- skills/            Procedural knowledge (how to answer domain questions)
- sources/           Raw source material
- sources/incoming/  Contributor drop zone
- library.yaml       Library schema, sources, skills, and tone rules
- log.md             Activity log

## How This Library Is Maintained

This library is the gestalt sum of knowledge about its domain. Pages are
synthesized from multiple evidence sources — not raw notes, but a living
understanding of facts plus informed operational opinions.

### Evidence Sources

1. **Daily logs** — human observations, synthesized via `+"`"+`lore maintain`+"`"+`
2. **Source repos** — infrastructure-as-code changes, tracked via `+"`"+`lore watch`+"`"+`
3. **Direct contributions** — published via `+"`"+`lore publish`+"`"+`

### Maintenance Workflows

- `+"`"+`lore maintain %s`+"`"+` — scans daily logs for new evidence, invokes agent to synthesize
- `+"`"+`lore watch %s`+"`"+` — scans source repos for commits, invokes agent to update pages
- `+"`"+`lore lint %s`+"`"+` — checks page health (frontmatter, TTL, format, local paths)
- `+"`"+`lore lint %s --fix`+"`"+` — auto-fixes section names, changelog format, local paths
- `+"`"+`lore index %s`+"`"+` — rebuilds Wiki/index.md

### CI Automation

When this library is in a git repo, these commands can run as GitHub Actions:

`+"`"+``+"`"+``+"`"+`yaml
# .github/workflows/maintain.yml
on:
  schedule:
    - cron: '0 6 * * 1'  # Weekly Monday 6am
  workflow_dispatch:
jobs:
  maintain:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: lore watch %s
      - run: lore lint %s --fix
      - run: lore index %s
      - uses: peter-evans/create-pull-request@v6
`+"`"+``+"`"+``+"`"+`

## Page Format Rules

When editing or creating pages, follow these conventions.

### Frontmatter

All pages must have YAML frontmatter. Field order matters:

**Service pages:** entity_type, aliases, last_updated, runbook, framework
**Environment pages:** entity_type, aliases, last_updated

### Required Sections

**Service pages must have:**
- `+"`"+`## What It Does`+"`"+`
- `+"`"+`## Known Issues`+"`"+`
- `+"`"+`## Change Log`+"`"+`

**Environment pages must have:**
- `+"`"+`## Inventory Data`+"`"+`
- `+"`"+`## Operational Notes`+"`"+`
- `+"`"+`## Incident History`+"`"+`

### Change Log Format

Use bullet lists, not tables:

`+"`"+``+"`"+``+"`"+`markdown
## Change Log

- 2026-04-14 — Description of what happened
- 2026-04-10 — Another event
`+"`"+``+"`"+``+"`"+`

### Prohibited Patterns

- No local filesystem paths (~/..., /Users/..., /home/...)
- No `+"`"+`## Known Issues and Quirks`+"`"+` — use `+"`"+`## Known Issues`+"`"+`
- No table-format Change Logs

## Contributing

Contributors drop raw observations into sources/incoming/ as markdown files.
The maintainer agent synthesizes these into Wiki pages.

Direct edits to Wiki/ should go through PR review.
`, filepath.Base(abs), today,
			filepath.Base(abs), filepath.Base(abs), filepath.Base(abs),
			filepath.Base(abs), filepath.Base(abs),
			filepath.Base(abs), filepath.Base(abs), filepath.Base(abs))
		if err := os.WriteFile(filepath.Join(abs, "CLAUDE.md"), []byte(claudeMd), 0o644); err != nil {
			return err
		}

		// Wiki/index.md
		indexMd := fmt.Sprintf(`---
last_updated: %s
tags:
  - wiki-index
---
# Index

Navigation layer for this library.
`, today)
		if err := os.WriteFile(filepath.Join(abs, "Wiki", "index.md"), []byte(indexMd), 0o644); err != nil {
			return err
		}

		// .gitignore
		gitignore := `# lore temp files
sources/incoming/
.lore/
`
		if err := os.WriteFile(filepath.Join(abs, ".gitignore"), []byte(gitignore), 0o644); err != nil {
			return err
		}

		// log.md
		logMd := `# Library Activity Log

Append-only record of maintenance operations.

---
`
		if err := os.WriteFile(filepath.Join(abs, "log.md"), []byte(logMd), 0o644); err != nil {
			return err
		}

		fmt.Printf("Library initialized at %s\n", abs)
		fmt.Println("\nNext steps:")
		fmt.Println("  1. Edit library.yaml (set name, description, tone rules)")
		fmt.Println("  2. Create a GHE repo and push this content")
		fmt.Println("  3. Run 'lore library register' to add it to the registry")
		return nil
	},
}

var libraryRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a library in the registry",
	Long:  `Not yet implemented — requires a registry to submit to.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Library registration is not yet implemented.")
		return nil
	},
}

func init() {
	libraryCmd.AddCommand(libraryInitCmd)
	libraryCmd.AddCommand(libraryRegisterCmd)
	libraryCmd.AddCommand(lintCmd)
	libraryCmd.AddCommand(indexCmd)
	libraryCmd.AddCommand(seedCmd)
	libraryCmd.AddCommand(publishCmd)
	libraryCmd.AddCommand(skillsCmd)
	libraryCmd.AddCommand(reviewCmd)
	libraryCmd.AddCommand(maintainCmd)
	libraryCmd.AddCommand(watchCmd)
}
