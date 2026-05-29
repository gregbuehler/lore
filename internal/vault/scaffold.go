package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gbuehler/lore/internal/config"
)

// ScaffoldOptions contains the user-provided configuration for vault init.
type ScaffoldOptions struct {
	Name          string
	Email         string
	Host          string
	Entities      []string // e.g., ["people", "services", "tooling", "infrastructure"]
	Adopt         bool
	AgentProvider string // claude, codex, none
}

// Scaffold creates a new vault directory structure.
// If Adopt is true, it adds lore config to an existing directory without
// overwriting any files that already exist.
func Scaffold(vaultPath string, opts ScaffoldOptions) error {
	abs, err := filepath.Abs(vaultPath)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	// Check if already initialized
	if _, err := os.Stat(config.ConfigPath(abs)); err == nil {
		return fmt.Errorf("vault already initialized at %s (found .lore/config.yaml)", abs)
	}

	// In adopt mode, the directory must already exist
	if opts.Adopt {
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("%s does not exist or is not a directory", abs)
		}
	}

	agentProvider := normalizeAgentProvider(opts.AgentProvider)
	if agentProvider == "" {
		return fmt.Errorf("unsupported agent provider %q (use claude, codex, or none)", opts.AgentProvider)
	}

	// Create directories
	dirs := []string{
		filepath.Join(abs, "Daily Log"),
		filepath.Join(abs, "Weekly Digest"),
		filepath.Join(abs, "Threads"),
		filepath.Join(abs, "Templates"),
		filepath.Join(abs, "sources"),
		filepath.Join(abs, config.LoreDir),
		filepath.Join(abs, config.LoreDir, "skills"),
	}
	if agentProvider == "claude" {
		dirs = append(dirs, filepath.Join(abs, ".claude", "commands"))
	}
	// Wiki subdirectories per entity type
	dirs = append(dirs, filepath.Join(abs, "Wiki"))
	for _, et := range opts.Entities {
		dirs = append(dirs, filepath.Join(abs, "Wiki", entityDirName(et)))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}

	today := time.Now().Format("2006-01-02")

	// === .lore/config.yaml ===
	cfg := &config.Config{
		Vault: config.VaultConfig{
			Path: abs,
		},
		DefaultHost: opts.Host,
		MetaIndex:   filepath.Join(config.LibrariesDir(), "meta-index.md"),
		Agent:       defaultAgentConfig(agentProvider),
	}
	if opts.Name != "" || opts.Email != "" {
		cfg.Identity = config.IdentityConfig{
			Name:  opts.Name,
			Email: opts.Email,
		}
	}
	if err := cfg.Save(abs); err != nil {
		return err
	}

	if agentProvider == "claude" {
		// === .claude/CLAUDE.md ===
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "CLAUDE.md"),
			generateClaudeMD(opts, today)); err != nil {
			return err
		}

		// === .claude/commands/ ===
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "daily-log.md"),
			generateDailyLogCommand(opts)); err != nil {
			return err
		}
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "weekly-digest.md"),
			generateWeeklyDigestCommand()); err != nil {
			return err
		}
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "lint.md"),
			generateLintCommand(opts)); err != nil {
			return err
		}
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "wiki-update.md"),
			generateWikiUpdateCommand(opts)); err != nil {
			return err
		}
		if err := writeIfNotExists(filepath.Join(abs, ".claude", "commands", "capture.md"),
			generateCaptureCommand()); err != nil {
			return err
		}
	}
	if agentProvider == "codex" {
		if err := writeIfNotExists(filepath.Join(abs, "AGENTS.md"),
			generateAgentsMD(opts, today)); err != nil {
			return err
		}
	}

	// === Templates/ ===
	if err := writeIfNotExists(filepath.Join(abs, "Templates", "Daily Log Template.md"),
		generateDailyLogTemplate()); err != nil {
		return err
	}
	if err := writeIfNotExists(filepath.Join(abs, "Templates", "Thread Template.md"),
		generateThreadTemplate()); err != nil {
		return err
	}

	// === Wiki/index.md ===
	if err := writeIfNotExists(filepath.Join(abs, "Wiki", "index.md"),
		generateWikiIndex(opts, today)); err != nil {
		return err
	}

	// === README.md ===
	if err := writeIfNotExists(filepath.Join(abs, "README.md"),
		generateReadme(opts)); err != nil {
		return err
	}

	// === log.md ===
	if err := writeIfNotExists(filepath.Join(abs, "log.md"),
		"# Vault Activity Log\n\nAppend-only record of lore operations.\n\n---\n"); err != nil {
		return err
	}

	// === .lore/skills/contribute-knowledge.md ===
	if err := writeIfNotExists(filepath.Join(abs, config.LoreDir, "skills", "contribute-knowledge.md"),
		generateContributeSkill()); err != nil {
		return err
	}

	return nil
}

func normalizeAgentProvider(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case "":
		return "claude"
	case "claude", "codex", "none":
		return normalized
	default:
		return ""
	}
}

func defaultAgentConfig(provider string) config.AgentConfig {
	switch provider {
	case "claude":
		return config.AgentConfig{Provider: "claude", Command: "claude"}
	case "codex":
		return config.AgentConfig{
			Provider: "codex",
			Command:  "codex",
			Sandbox:  "workspace-write",
			Approval: "never",
		}
	case "none":
		return config.AgentConfig{Provider: "none"}
	default:
		return config.AgentConfig{}
	}
}

func writeIfNotExists(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// entityDirName returns the Wiki subdirectory name for an entity type.
// Capitalizes the first letter and adds trailing "s" if not plural.
func entityDirName(entityType string) string {
	et := strings.TrimSpace(entityType)
	switch et {
	case "person", "people":
		return "People"
	case "service", "services":
		return "Services"
	case "tool", "tooling":
		return "Tooling"
	case "infrastructure":
		return "Infrastructure"
	case "environment", "environments":
		return "Environments"
	case "customer", "customers":
		return "Customers"
	case "vendor", "vendors":
		return "Vendors"
	case "organization", "organizations":
		return "Organizations"
	case "concept", "concepts":
		return "Concepts"
	default:
		// Capitalize first letter
		if len(et) == 0 {
			return et
		}
		return strings.ToUpper(et[:1]) + et[1:]
	}
}

// entityTypeName returns the singular entity_type value for frontmatter.
func entityTypeName(entityType string) string {
	et := strings.TrimSpace(entityType)
	switch et {
	case "people", "person":
		return "person"
	case "services", "service":
		return "service"
	case "tooling", "tool":
		return "tool"
	case "infrastructure":
		return "infrastructure"
	case "environments", "environment":
		return "environment"
	case "customers", "customer":
		return "customer"
	case "vendors", "vendor":
		return "vendor"
	case "organizations", "organization":
		return "organization"
	case "concepts", "concept":
		return "concept"
	default:
		return et
	}
}

// ===========================================================================
// Generators
// ===========================================================================

func generateClaudeMD(opts ScaffoldOptions, today string) string {
	var b strings.Builder

	ownerLine := "the vault owner"
	if opts.Name != "" {
		ownerLine = opts.Name
	}

	b.WriteString("# Lore Vault\n\n")
	b.WriteString(fmt.Sprintf("Personal knowledge vault for %s. Initialized %s.\n\n", ownerLine, today))

	// Structure
	b.WriteString("## Vault Structure\n\n")
	b.WriteString("```\n")
	b.WriteString("Daily Log/YYYY-MM/YYYY-MM-DD.md   — Daily journal entries\n")
	b.WriteString("Weekly Digest/YYYY-WXX.md         — Weekly impact summaries\n")
	b.WriteString("Wiki/                             — Entity memory (LLM-maintained)\n")
	b.WriteString("  Wiki/index.md                   — Navigation layer; read this first\n")
	for _, et := range opts.Entities {
		dir := entityDirName(et)
		b.WriteString(fmt.Sprintf("  Wiki/%s/\n", dir))
	}
	b.WriteString("Threads/                          — Ongoing efforts, incidents, workstreams\n")
	b.WriteString("Templates/                        — Document templates\n")
	b.WriteString("sources/                          — Raw source material\n")
	b.WriteString("log.md                            — Append-only activity log\n")
	b.WriteString(".claude/commands/                 — Slash commands\n")
	b.WriteString(".lore/                            — CLI config, vault-level skills\n")
	b.WriteString("```\n\n")

	// Slash commands
	b.WriteString("## Slash Commands\n\n")
	b.WriteString("- `/daily-log [date]` — Generate or update a daily journal entry for a given date (default: today)\n")
	b.WriteString("- `/weekly-digest` — Synthesize the week's daily logs into an impact digest\n")
	b.WriteString("- `/lint` — Audit the vault for orphan pages, stale items, frontmatter errors, and index gaps\n")
	b.WriteString("- `/wiki-update` — Full Wiki rebuild from all daily logs\n")
	b.WriteString("- `/capture` — Summarize this session's work and append it to today's daily log\n\n")

	// Wiki layer
	b.WriteString("## Wiki Layer (Entity Memory)\n\n")
	b.WriteString("The `Wiki/` directory is the persistent entity memory for this vault.\n")
	b.WriteString("It accumulates context across daily log entries so Claude does not need\n")
	b.WriteString("to re-derive this context each session.\n\n")
	b.WriteString("**Read `Wiki/index.md` first** before answering any question about vault\n")
	b.WriteString("entities, active projects, or recent work.\n\n")

	// Taxonomy table
	b.WriteString("### Taxonomy\n\n")
	b.WriteString("| Directory | entity_type | What goes here |\n")
	b.WriteString("|---|---|---|\n")
	for _, et := range opts.Entities {
		dir := entityDirName(et)
		typ := entityTypeName(et)
		desc := entityDescription(et)
		b.WriteString(fmt.Sprintf("| `Wiki/%s/` | `%s` | %s |\n", dir, typ, desc))
	}
	b.WriteString("\n")

	// Detection heuristics
	b.WriteString("### Entity Detection Heuristics\n\n")
	b.WriteString("<!-- CUSTOMIZE: Add patterns specific to your domain -->\n\n")
	b.WriteString("When scanning a daily log entry for entities to update:\n\n")
	for _, et := range opts.Entities {
		dir := entityDirName(et)
		b.WriteString(fmt.Sprintf("- **%s:** Match against `Wiki/%s/` filenames and aliases in frontmatter.\n", dir, dir))
	}
	b.WriteString("\n")

	// Threads
	b.WriteString("## Threads (Ongoing Efforts)\n\n")
	b.WriteString("`Threads/` captures ongoing work that spans multiple days: incidents,\n")
	b.WriteString("customer engagements, epics, investigations, RFCs, or any workstream that\n")
	b.WriteString("deserves its own running notebook.\n\n")
	b.WriteString("Unlike daily logs (one per day) and Wiki pages (stable entity knowledge),\n")
	b.WriteString("threads have an **open/close lifecycle** — they start when the effort begins\n")
	b.WriteString("and close when it concludes.\n\n")
	b.WriteString("### Thread Frontmatter\n\n")
	b.WriteString("```yaml\n")
	b.WriteString("---\n")
	b.WriteString("title: <descriptive name>\n")
	b.WriteString("status: active | closed\n")
	b.WriteString("private: false\n")
	b.WriteString("started: YYYY-MM-DD\n")
	b.WriteString("closed:  # YYYY-MM-DD when resolved\n")
	b.WriteString("tags: []  # e.g., incident, customer, epic, rfc, investigation\n")
	b.WriteString("---\n")
	b.WriteString("```\n\n")
	b.WriteString("### Privacy\n\n")
	b.WriteString("Threads with `private: true` **must never** be shared upstream to libraries,\n")
	b.WriteString("referenced in library PRs, or included in any output that leaves the vault.\n")
	b.WriteString("The contribute-knowledge skill, `/weekly-digest`, and all sharing workflows\n")
	b.WriteString("must check this flag and skip private threads entirely.\n\n")
	b.WriteString("Examples of private threads: self-evaluations, promotion cases, compensation\n")
	b.WriteString("notes, organizational strategy, personnel observations, feedback drafts.\n\n")
	b.WriteString("### Thread Lifecycle\n\n")
	b.WriteString("1. **Open** — Create `Threads/<slug>.md` with `status: active`\n")
	b.WriteString("2. **Update** — Append notes as the effort progresses; daily-log cross-links\n")
	b.WriteString("3. **Close** — Set `status: closed` and `closed: YYYY-MM-DD`\n")
	b.WriteString("4. **Graduate** — Stable facts from closed threads should be synthesized\n")
	b.WriteString("   into Wiki entity pages (if not private)\n\n")
	b.WriteString("### Cross-linking\n\n")
	b.WriteString("When writing a daily log, scan for active threads by reading `Threads/`\n")
	b.WriteString("for files with `status: active`. If the day's work relates to an active\n")
	b.WriteString("thread, add a cross-reference in both directions:\n")
	b.WriteString("- Daily log: `→ [[Threads/<slug>]]`\n")
	b.WriteString("- Thread: `- [[Daily Log/YYYY-MM/YYYY-MM-DD]] — what happened`\n\n")

	// Data sources
	b.WriteString("## Data Sources for Daily Logs\n\n")
	b.WriteString("The `/daily-log` command processes raw text from any source. Common setups:\n\n")
	b.WriteString("- **Slack export** — script that pulls your DMs/channels via Slack API\n")
	b.WriteString("- **Clipboard paste** — copy-paste from Slack, email, meeting notes\n")
	b.WriteString("- **Git activity** — `git log --author=<you> --since=<date>` across repos\n")
	b.WriteString("- **Calendar** — meeting titles and attendees for the day\n")
	b.WriteString("- **Jira / Linear** — tickets touched that day\n\n")
	b.WriteString("To wire a data source, add a fetch script to `.scripts/` and reference\n")
	b.WriteString("it from `.claude/commands/daily-log.md`.\n\n")

	// Wiki frontmatter
	b.WriteString("## Wiki Frontmatter Schema\n\n")
	b.WriteString("All entity pages in `Wiki/` use this frontmatter:\n\n")
	b.WriteString("```yaml\n")
	b.WriteString("---\n")
	b.WriteString("entity_type: " + entityTypeName(opts.Entities[0]))
	for _, et := range opts.Entities[1:] {
		b.WriteString(" | " + entityTypeName(et))
	}
	b.WriteString("\n")
	b.WriteString("aliases: []\n")
	b.WriteString("last_updated: YYYY-MM-DD\n")
	b.WriteString("---\n")
	b.WriteString("```\n\n")

	// Conventions
	b.WriteString("## Conventions\n\n")
	b.WriteString("<!-- CUSTOMIZE: Add your team's conventions -->\n\n")
	b.WriteString("- **File naming:** Daily logs `YYYY-MM-DD.md`, weekly digests `YYYY-WXX.md`\n")
	b.WriteString("- **Internal links:** `[[Daily Log/YYYY-MM/YYYY-MM-DD]]`\n")
	b.WriteString("- **External links:** Standard markdown `[description](url)`\n")
	b.WriteString("- **People:** Referenced by username\n\n")

	// Key context
	b.WriteString("## Key Context\n\n")
	b.WriteString("<!-- CUSTOMIZE: Describe who you are and what you work on -->\n\n")
	if opts.Name != "" {
		b.WriteString(fmt.Sprintf("- Vault owner: %s\n", opts.Name))
	}
	b.WriteString("- Primary systems: <!-- e.g., Kubernetes, AWS, React -->\n")
	b.WriteString("- Team: <!-- e.g., SRE, Backend, Platform -->\n\n")

	// Lore import
	b.WriteString("@../.lore/LORE.md\n")

	return b.String()
}

func generateAgentsMD(opts ScaffoldOptions, today string) string {
	var b strings.Builder

	ownerLine := "the vault owner"
	if opts.Name != "" {
		ownerLine = opts.Name
	}

	b.WriteString("# Lore Vault\n\n")
	b.WriteString(fmt.Sprintf("Personal knowledge vault for %s. Initialized %s.\n\n", ownerLine, today))
	b.WriteString("## Context\n\n")
	b.WriteString("Read the lore-generated vault context before answering questions about subscribed libraries, vault skills, or maintenance workflows.\n\n")
	b.WriteString("@.lore/LORE.md\n\n")
	b.WriteString("## Vault Structure\n\n")
	b.WriteString("```\n")
	b.WriteString("Daily Log/YYYY-MM/YYYY-MM-DD.md   - Daily journal entries\n")
	b.WriteString("Weekly Digest/YYYY-WXX.md         - Weekly impact summaries\n")
	b.WriteString("Wiki/                             - Entity memory; read Wiki/index.md first\n")
	for _, et := range opts.Entities {
		dir := entityDirName(et)
		b.WriteString(fmt.Sprintf("  Wiki/%s/\n", dir))
	}
	b.WriteString("Threads/                          - Ongoing efforts, incidents, workstreams\n")
	b.WriteString("Templates/                        - Document templates\n")
	b.WriteString("sources/                          - Raw source material\n")
	b.WriteString("log.md                            - Append-only activity log\n")
	b.WriteString(".lore/                            - CLI config, vault-level skills\n")
	b.WriteString("```\n")

	return b.String()
}

func entityDescription(et string) string {
	switch strings.TrimSpace(et) {
	case "people", "person":
		return "People you interact with"
	case "services", "service":
		return "Services and microservices"
	case "tooling", "tool":
		return "Developer and operational tools"
	case "infrastructure":
		return "Cloud and platform infrastructure"
	case "environments", "environment":
		return "Named deployment environments"
	case "customers", "customer":
		return "Customer or program entities"
	case "vendors", "vendor":
		return "External third-party systems"
	case "organizations", "organization":
		return "Teams, orgs, and working groups"
	case "concepts", "concept":
		return "Cross-cutting concepts"
	default:
		return "<!-- describe what goes here -->"
	}
}

func generateDailyLogCommand(opts ScaffoldOptions) string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("description: generate or update a daily journal entry for a given date (default today)\n")
	b.WriteString("---\n")
	b.WriteString("Generate a daily log entry. The target date is: **$ARGUMENTS** (if blank, use today's date).\n\n")

	b.WriteString("## 1. Determine the Target Date\n\n")
	b.WriteString("- If `$ARGUMENTS` is empty, use today's date (YYYY-MM-DD).\n")
	b.WriteString("- If `$ARGUMENTS` is a date string, parse it to YYYY-MM-DD.\n\n")

	b.WriteString("## 2. Gather Source Material\n\n")
	b.WriteString("<!-- CUSTOMIZE: Add your data source steps here. Examples:\n")
	b.WriteString("     - python3 .scripts/fetch_slack_day.py YYYY-MM-DD\n")
	b.WriteString("     - Ask the user to paste their notes\n")
	b.WriteString("     - Query a Jira/Linear API\n")
	b.WriteString("     - Run git log --author=<you> --since=YYYY-MM-DD --until=YYYY-MM-DD+1 --oneline\n")
	b.WriteString("-->\n\n")
	b.WriteString("Ask the user to provide or paste the day's activity if no automated source\n")
	b.WriteString("is configured. Accept raw text from any source — Slack messages, meeting notes,\n")
	b.WriteString("git log output, ticket updates, etc.\n\n")

	b.WriteString("## 3. Read Existing Journal (if any)\n\n")
	b.WriteString("The journal file lives at: `./Daily Log/YYYY-MM/YYYY-MM-DD.md`\n\n")
	b.WriteString("- If the file exists, read it and **preserve all existing content** — merge, don't replace.\n")
	b.WriteString("- If the file doesn't exist, create it from `Templates/Daily Log Template.md`.\n\n")

	b.WriteString("## 4. Deduplicate Action Items\n\n")
	b.WriteString("Search recent daily logs (last 30 days) for existing action items:\n\n")
	b.WriteString("```bash\n")
	b.WriteString("find \"./Daily Log\" -name \"*.md\" -mtime -30 -exec grep -H \"\\- \\[ \\]\" {} \\;\n")
	b.WriteString("find \"./Daily Log\" -name \"*.md\" -mtime -30 -exec grep -H \"\\- \\[x\\]\" {} \\;\n")
	b.WriteString("```\n\n")
	b.WriteString("Do not re-add items that already appear unchecked or checked in recent journals.\n\n")

	b.WriteString("## 5. Write the Journal Entry\n\n")
	b.WriteString("Read `Templates/Daily Log Template.md` for the document structure. Use it exactly.\n\n")
	b.WriteString("### Writing Rules\n\n")
	b.WriteString("**What Happened?**\n")
	b.WriteString("- Each bullet: `* **Topic** — description`\n")
	b.WriteString("- Group related conversations into a single bullet\n")
	b.WriteString("- Be concise but capture substance — decisions, positions, problems solved\n")
	b.WriteString("- Name people involved\n")
	b.WriteString("- Skip casual/social chatter\n\n")
	b.WriteString("**Quick References**\n")
	b.WriteString("- Markdown links: `- [description](url)`\n")
	b.WriteString("- Extract PRs, docs, dashboards, and any shared links\n\n")
	b.WriteString("**Action Items**\n")
	b.WriteString("- Markdown checkboxes: `- [ ] item`\n")
	b.WriteString("- Only items requiring follow-up by you\n")
	b.WriteString("- Deduplicate against recent journals (see step 4)\n\n")

	b.WriteString("## 6. Entity Detection\n\n")
	b.WriteString("Scan the \"What Happened?\" section for known entities. Match against\n")
	b.WriteString("`Wiki/index.md` entity tables — check canonical names and aliases.\n\n")

	for _, et := range opts.Entities {
		dir := entityDirName(et)
		b.WriteString(fmt.Sprintf("- **%s:** Match against `Wiki/%s/` filenames and frontmatter aliases.\n", dir, dir))
	}
	b.WriteString("\n")

	b.WriteString("## 7. Update Entity Pages\n\n")
	b.WriteString("For each detected entity with an existing Wiki page:\n\n")
	b.WriteString("1. Read the entity page\n")
	b.WriteString("2. Add an entry to `## Change Log`: `- [[Daily Log/YYYY-MM/YYYY-MM-DD]] — summary`\n")
	b.WriteString("3. Update factual claims if the log clarifies or contradicts existing content\n")
	b.WriteString("4. Set `last_updated` in frontmatter to today's date\n\n")
	b.WriteString("For entities appearing 2+ times with no existing page: create the page with the\n")
	b.WriteString("correct `entity_type` and what is known.\n\n")
	b.WriteString("**Constraint:** Updates must be additive and factual. Do not remove existing content.\n\n")

	b.WriteString("## 8. Thread Cross-linking\n\n")
	b.WriteString("Scan `Threads/` for files with `status: active` in frontmatter.\n")
	b.WriteString("If any of the day's work relates to an active thread:\n\n")
	b.WriteString("1. Add `→ [[Threads/<slug>]]` to the relevant bullet in the daily log\n")
	b.WriteString("2. Append `- [[Daily Log/YYYY-MM/YYYY-MM-DD]] — summary` to the thread file\n\n")
	b.WriteString("Match by thread title, tags, or entity overlap with the log entry.\n\n")

	b.WriteString("## 9. Update Wiki/index.md\n\n")
	b.WriteString("- Add any new entity pages to the appropriate table\n")
	b.WriteString("- Update `## Recent Daily Logs` (keep 7 most recent)\n")
	b.WriteString("- Set `last_updated` in frontmatter\n\n")

	b.WriteString("## 10. Append to log.md\n\n")
	b.WriteString("```\n")
	b.WriteString("YYYY-MM-DDTHH:MM /daily-log YYYY-MM-DD — N bullets, N links, N action items; updated N entity pages; refreshed index.md\n")
	b.WriteString("```\n")

	return b.String()
}

func generateWeeklyDigestCommand() string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("description: synthesize daily logs into a weekly impact digest\n")
	b.WriteString("---\n")
	b.WriteString("Synthesize this week's daily logs into a digest with a shareable summary\n")
	b.WriteString("and a detailed reference log.\n\n")

	b.WriteString("## 1. Read Source Material\n\n")
	b.WriteString("Read all daily logs for the current ISO week (Monday–Friday).\n")
	b.WriteString("If today is early in the week, fall back to the most recent complete week.\n\n")

	b.WriteString("## 2. Check Thread Privacy\n\n")
	b.WriteString("Read all `Threads/` files referenced by this week's daily logs.\n")
	b.WriteString("Any thread with `private: true` must be **excluded** from the Summary\n")
	b.WriteString("section (which is shareable). Private thread work may appear in the\n")
	b.WriteString("Detailed Log (which is personal reference only) but must be clearly\n")
	b.WriteString("marked `(private)` so it is never copy-pasted into Slack.\n\n")

	b.WriteString("## 3. Classify Work: Planned vs. Ad-hoc\n\n")
	b.WriteString("Every item must be classified:\n\n")
	b.WriteString("- **Planned** = work intentionally scheduled before the week began\n")
	b.WriteString("- **Ad-hoc** = work that emerged during the week (incidents, requests, reactive fixes)\n\n")
	b.WriteString("When in doubt, classify as ad-hoc.\n\n")

	b.WriteString("## 4. Write the Detailed Log\n\n")
	b.WriteString("The `### Detailed Log` section is your own reference:\n\n")
	b.WriteString("- Cover all significant work, grouped by workstream (not by day)\n")
	b.WriteString("- Separate into **Planned** and **Ad-hoc** subsections\n")
	b.WriteString("- Include PR numbers, names, environment names — enough to jog memory months later\n")
	b.WriteString("- Mark private thread work with `(private)` — do not include in Summary\n\n")

	b.WriteString("## 5. Write the Summary\n\n")
	b.WriteString("The `### Summary` section is for sharing with your lead/skip-level:\n\n")
	b.WriteString("- Two subsections: **Planned** and **Ad-hoc** (show `- (none)` if empty)\n")
	b.WriteString("- Max 5 bullets total\n")
	b.WriteString("- Each bullet: one sentence, strong active verb, rank by impact\n")
	b.WriteString("- Do not inflate your role — be accurate about who drove what\n\n")

	b.WriteString("## 6. Save the Digest\n\n")
	b.WriteString("File path: `./Weekly Digest/YYYY-WXX.md`\n\n")
	b.WriteString("```markdown\n")
	b.WriteString("---\n")
	b.WriteString("tags:\n")
	b.WriteString("  - weekly-digest\n")
	b.WriteString("week: \"YYYY-WXX\"\n")
	b.WriteString("date_range: \"YYYY-MM-DD to YYYY-MM-DD\"\n")
	b.WriteString("---\n")
	b.WriteString("## Weekly Digest — YYYY-WXX\n\n")
	b.WriteString("### Summary\n\n")
	b.WriteString("**Planned**\n- ...\n\n")
	b.WriteString("**Ad-hoc**\n- ...\n\n")
	b.WriteString("---\n\n")
	b.WriteString("### Detailed Log\n\n")
	b.WriteString("**Planned**\n- ...\n\n")
	b.WriteString("**Ad-hoc**\n- ...\n\n")
	b.WriteString("---\n")
	b.WriteString("### Source Logs\n")
	b.WriteString("- [[Daily Log/YYYY-MM/YYYY-MM-DD]]\n")
	b.WriteString("```\n\n")

	b.WriteString("Print the Summary section to the conversation (for pasting into Slack).\n\n")

	b.WriteString("## 7. Update Wiki and Log\n\n")
	b.WriteString("- Update `Wiki/index.md` → `## Recent Weekly Digests` (keep 4 most recent)\n")
	b.WriteString("- Append to `log.md`:\n")
	b.WriteString("  ```\n")
	b.WriteString("  YYYY-MM-DDTHH:MM /weekly-digest — WXX, N summary bullets, N detailed items, N source logs\n")
	b.WriteString("  ```\n")

	return b.String()
}

func generateLintCommand(opts ScaffoldOptions) string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("description: audit the vault for orphan pages, stale items, frontmatter errors, and index gaps\n")
	b.WriteString("---\n")
	b.WriteString("Run a vault audit. Report only — do not modify files (except appending to log.md).\n\n")

	b.WriteString("## 1. Orphan Pages\n\n")
	b.WriteString("Scan all markdown files in `Wiki/` and `Threads/` for any file not linked\n")
	b.WriteString("from another vault file. Exclude `Wiki/index.md` and `Templates/`.\n\n")

	b.WriteString("## 2. Stale Open Action Items\n\n")
	b.WriteString("Search Daily Log files for unchecked action items (`- [ ]`). Flag any in\n")
	b.WriteString("files older than 14 days.\n\n")

	b.WriteString("## 3. Stale Active Threads\n\n")
	b.WriteString("Scan `Threads/` for files with `status: active` in frontmatter.\n")
	b.WriteString("Flag any thread with `started` more than 30 days ago that has no\n")
	b.WriteString("daily log cross-references in the last 14 days.\n\n")

	b.WriteString("## 4. Frontmatter Audit\n\n")
	b.WriteString("Scan all entity pages in `Wiki/` for:\n")
	b.WriteString("- Missing `entity_type` field\n")
	b.WriteString("- Missing `last_updated` field\n")
	b.WriteString("- `last_updated` more than 30 days old\n\n")
	b.WriteString("Scan all threads in `Threads/` for:\n")
	b.WriteString("- Missing `status` field\n")
	b.WriteString("- Missing `started` field\n")
	b.WriteString("- Missing `private` field\n\n")

	b.WriteString("## 5. Wiki Index Gaps\n\n")
	b.WriteString("For each file in Wiki/ subdirectories:\n")
	b.WriteString("- Check whether it's listed in `Wiki/index.md`\n")
	b.WriteString("- Report any entity page missing from the index\n\n")

	b.WriteString("## 6. Output\n\n")
	b.WriteString("Print a structured report with one section per check.\n")
	b.WriteString("If a check finds no issues, print `(none)` — do not omit the section.\n\n")
	b.WriteString("End with:\n")
	b.WriteString("```\n")
	b.WriteString("Checks run: 5 | Issues found: N\n")
	b.WriteString("```\n\n")
	b.WriteString("Append one line to `log.md`:\n")
	b.WriteString("```\n")
	b.WriteString("YYYY-MM-DDTHH:MM /lint — N orphans, N stale items, N stale threads, N frontmatter errors, N index gaps\n")
	b.WriteString("```\n")

	return b.String()
}

func generateWikiUpdateCommand(opts ScaffoldOptions) string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("description: full wiki refresh — re-scan daily logs, rebuild entity pages, refresh index\n")
	b.WriteString("---\n")
	b.WriteString("Run a full Wiki rebuild from all existing daily logs.\n\n")

	b.WriteString("## 1. Read Current Wiki State\n\n")
	b.WriteString("Read `Wiki/index.md` to establish the entity inventory. List all existing\n")
	b.WriteString("entity pages and note each page's `last_updated` date.\n\n")

	b.WriteString("## 2. Scan All Daily Logs\n\n")
	b.WriteString("Read all `.md` files in `Daily Log/` in chronological order. For each log:\n\n")
	b.WriteString("- Detect entities using the heuristics in CLAUDE.md\n")
	b.WriteString("- Build a per-entity list of mentions with dates and one-line summaries\n\n")

	b.WriteString("## 3. Update Entity Pages\n\n")
	b.WriteString("For each entity with a Wiki page:\n")
	b.WriteString("- Compare `last_updated` against the most recent log mentioning it\n")
	b.WriteString("- If behind, update with new log evidence\n")
	b.WriteString("- Update `last_updated` in frontmatter\n\n")
	b.WriteString("For entities in 2+ logs with no page: create a new page.\n\n")
	b.WriteString("**Constraint:** Updates must be additive. Do not remove manually-added content.\n\n")

	b.WriteString("## 4. Refresh Wiki/index.md\n\n")
	b.WriteString("- Rebuild `## Recent Daily Logs` (last 7)\n")
	b.WriteString("- Rebuild `## Recent Weekly Digests` (last 4)\n")
	b.WriteString("- Rebuild `## Active Threads` from `Threads/` files with `status: active`\n")
	b.WriteString("- Ensure all entity pages are listed in entity tables\n")
	b.WriteString("- Update `last_updated`\n\n")

	b.WriteString("## 5. Report and Log\n\n")
	b.WriteString("Print summary: logs scanned, pages updated, pages created.\n\n")
	b.WriteString("Append to `log.md`:\n")
	b.WriteString("```\n")
	b.WriteString("YYYY-MM-DDTHH:MM /wiki-update — N logs scanned, N pages updated, N created, index refreshed\n")
	b.WriteString("```\n")

	return b.String()
}

func generateDailyLogTemplate() string {
	return `---
tags:
  - daily-log
---
## What Happened?

*

---
## Quick References (Tickets, Docs, and Links)

-

---

## Action Items

-

---
`
}

func generateThreadTemplate() string {
	return `---
title:
status: active
private: false
started:
closed:
tags: []
---
## Context

<!-- What is this effort about? Why was it started? -->

## Log

<!-- Append dated entries as the effort progresses -->

## Outcome

<!-- Fill in when closing the thread -->
`
}

func generateWikiIndex(opts ScaffoldOptions, today string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("---\nlast_updated: %s\ntags:\n  - wiki-index\n---\n", today))
	b.WriteString("# Wiki Index\n\n")
	b.WriteString("Navigation layer for the vault. Entity pages live in Wiki/ subdirectories.\n\n")

	b.WriteString("## Recent Daily Logs\n\n")
	b.WriteString("| Date | Key Topics |\n")
	b.WriteString("|---|---|\n")
	b.WriteString("| (none yet) | |\n\n")

	b.WriteString("## Recent Weekly Digests\n\n")
	b.WriteString("| Week | Highlights |\n")
	b.WriteString("|---|---|\n")
	b.WriteString("| (none yet) | |\n\n")

	b.WriteString("## Active Threads\n\n")
	b.WriteString("| Thread | Started | Tags |\n")
	b.WriteString("|---|---|---|\n")
	b.WriteString("| (no active threads) | | |\n\n")

	b.WriteString("## Entity Pages\n\n")
	for _, et := range opts.Entities {
		dir := entityDirName(et)
		typ := entityTypeName(et)
		b.WriteString(fmt.Sprintf("### %s\n\n", dir))
		b.WriteString(fmt.Sprintf("| Page | Type | Aliases |\n"))
		b.WriteString("|---|---|---|\n")
		b.WriteString(fmt.Sprintf("| (no %s pages yet) | %s | |\n\n", typ, typ))
	}

	return b.String()
}

func generateReadme(opts ScaffoldOptions) string {
	var b strings.Builder

	ownerLine := "your"
	if opts.Name != "" {
		ownerLine = opts.Name + "'s"
	}

	b.WriteString(fmt.Sprintf("# %s Lore Vault\n\n", ownerLine))
	b.WriteString("A personal knowledge vault managed by [lore](https://github.com/gbuehler/lore).\n\n")
	b.WriteString("## What is this?\n\n")
	b.WriteString("This vault is a structured, markdown-based knowledge system designed to be\n")
	b.WriteString("operated by both you and an LLM agent (Claude). It captures daily work,\n")
	b.WriteString("accumulates entity knowledge over time, and connects to shared team libraries.\n\n")

	// Structure
	b.WriteString("## Structure\n\n")
	b.WriteString("```\n")
	b.WriteString("Daily Log/        Daily journal entries (one per working day)\n")
	b.WriteString("Weekly Digest/    Weekly impact summaries (shareable with your lead)\n")
	b.WriteString("Threads/          Ongoing efforts — incidents, epics, investigations, RFCs\n")
	b.WriteString("Wiki/             Entity memory — accumulated knowledge about people,\n")
	b.WriteString("                  services, tools, etc. maintained by the agent\n")
	b.WriteString("Templates/        Document templates for daily logs, etc.\n")
	b.WriteString("sources/          Raw source material (Slack exports, meeting notes, etc.)\n")
	b.WriteString("log.md            Append-only activity log of vault operations\n")
	b.WriteString(".claude/          Agent configuration and slash commands\n")
	b.WriteString(".lore/            CLI config, vault-level skills, library subscriptions\n")
	b.WriteString("```\n\n")

	// Information lifecycle
	b.WriteString("## Information Lifecycle\n\n")
	b.WriteString("Information flows through the vault in stages:\n\n")
	b.WriteString("```\n")
	b.WriteString("raw input ──→ Daily Log ──→ Wiki pages ──→ shared libraries\n")
	b.WriteString("                  │          (accumulated)   (team knowledge)\n")
	b.WriteString("                  └──→ Threads\n")
	b.WriteString("                       (ongoing efforts)\n")
	b.WriteString("```\n\n")
	b.WriteString("1. **Capture** — Raw signal lands in `Daily Log/` via `/daily-log`. Sources\n")
	b.WriteString("   can be anything: Slack messages, git activity, meeting notes, incident\n")
	b.WriteString("   observations. The agent extracts structure from unstructured input.\n\n")
	b.WriteString("2. **Track** — Multi-day efforts get a `Threads/` page: incidents,\n")
	b.WriteString("   customer engagements, epics, investigations. Daily logs cross-link to\n")
	b.WriteString("   active threads automatically. Threads open when the effort starts and\n")
	b.WriteString("   close when it concludes.\n\n")
	b.WriteString("3. **Accumulate** — As daily logs mention entities (people, services, tools),\n")
	b.WriteString("   the agent creates and updates `Wiki/` pages. These pages are the vault's\n")
	b.WriteString("   persistent memory — they survive across sessions and prevent the agent\n")
	b.WriteString("   from re-deriving context every conversation.\n\n")
	b.WriteString("4. **Synthesize** — `/weekly-digest` distills the week's logs into a\n")
	b.WriteString("   shareable summary (planned vs. ad-hoc work) and a detailed reference.\n\n")
	b.WriteString("5. **Contribute** — Knowledge that belongs to the team (not just you) gets\n")
	b.WriteString("   pushed upstream to shared lore libraries. The `contribute-knowledge`\n")
	b.WriteString("   skill handles this — direct push or PR depending on library policy.\n\n")

	// Workflows
	b.WriteString("## Daily Workflows\n\n")
	b.WriteString("### End of day: capture\n\n")
	b.WriteString("Run `/daily-log` in Claude Code. Paste or pipe in your day's activity.\n")
	b.WriteString("The agent will:\n")
	b.WriteString("- Extract key events, decisions, and action items\n")
	b.WriteString("- Detect known entities and update their Wiki pages\n")
	b.WriteString("- Update `Wiki/index.md` with the new log entry\n\n")

	b.WriteString("### End of week: synthesize\n\n")
	b.WriteString("Run `/weekly-digest`. The agent reads the week's daily logs and produces\n")
	b.WriteString("a summary you can paste into Slack or send to your lead.\n\n")

	b.WriteString("### As needed: maintain\n\n")
	b.WriteString("- `/lint` — audit for orphan pages, stale action items, frontmatter issues\n")
	b.WriteString("- `/wiki-update` — full Wiki rebuild from all daily logs\n")
	b.WriteString("- `lore vault context` — regenerate library discovery context\n")
	b.WriteString("- `lore library maintain <lib>` — synthesize new evidence into library pages\n\n")

	// Libraries
	b.WriteString("## Shared Libraries\n\n")
	b.WriteString("Vaults connect to shared, git-backed knowledge libraries via `lore subscribe`.\n")
	b.WriteString("Libraries are team-level knowledge bases — entity pages curated collectively.\n\n")
	b.WriteString("```bash\n")
	b.WriteString("lore subscribe <org/repo>     # subscribe to a library\n")
	b.WriteString("lore update                   # pull latest from all libraries\n")
	b.WriteString("lore vault status             # see subscription health\n")
	b.WriteString("lore vault context            # regenerate .lore/LORE.md with library info\n")
	b.WriteString("```\n\n")
	b.WriteString("When subscribed, the agent can read library pages for context and push\n")
	b.WriteString("new knowledge upstream when you learn something the team should know.\n\n")

	// Threads
	b.WriteString("## Threads\n\n")
	b.WriteString("`Threads/` tracks ongoing efforts: incidents, customer engagements, epics,\n")
	b.WriteString("investigations, RFCs — any work that spans multiple days and deserves its\n")
	b.WriteString("own running notebook. Each thread has `status: active` or `closed` and a\n")
	b.WriteString("`private: true/false` flag.\n\n")
	b.WriteString("Private threads never leave the vault — they're excluded from library\n")
	b.WriteString("contributions, weekly digests shared externally, and all other sharing\n")
	b.WriteString("workflows. Use `private: true` for sensitive meta-work, personnel topics,\n")
	b.WriteString("or anything that should stay personal (e.g., a self-evaluation, a promotion\n")
	b.WriteString("case, organizational strategy notes).\n\n")
	b.WriteString("When a thread closes, its stable facts should be synthesized into Wiki\n")
	b.WriteString("entity pages (unless private).\n\n")

	// Getting started
	b.WriteString("## Getting Started\n\n")
	b.WriteString("1. Review and customize `.claude/CLAUDE.md` — especially the `<!-- CUSTOMIZE -->` sections\n")
	b.WriteString("2. Subscribe to any shared libraries: `lore subscribe <org/repo>`\n")
	b.WriteString("3. Run `lore vault context` to wire up library discovery\n")
	b.WriteString("4. Start capturing: `/daily-log` in Claude Code\n")

	return b.String()
}

func generateContributeSkill() string {
	return `---
name: contribute-knowledge
description: Push new knowledge from the vault upstream into a shared library
trigger: update the library | contribute this to {library} | push this upstream
inputs:
  - name: library
    description: Target library name (e.g., my-environments, services)
    required: true
  - name: entity
    description: The entity being updated (e.g., staging, gateway)
    required: false
---

# Contribute Knowledge

Push new knowledge from the vault upstream into a shared library page.

## Step 0: Check privacy

Before contributing ANY content upstream, check the source:

- If the knowledge comes from a thread with ` + "`private: true`" + `, **STOP**.
  Private thread content must never leave the vault.
- If the knowledge came from a daily log entry that cross-links a private
  thread, scrub any details traceable to that thread before contributing.

## Step 1: Identify what's new

Determine what new information needs to go upstream. Common sources:

- **Conversation context** — the user just told you something new about an entity
- **Daily log evidence** — ` + "`lore library review <library>`" + ` surfaces unincorporated mentions
- **Incident observations** — real-time operational context not yet in the library

## Step 2: Read the current library page

Understand what's already documented. Don't duplicate existing content.
Check the page's tone and structure — your contribution should match.

## Step 3: Read the library's tone rules

Check the library CLAUDE.md and library.yaml for voice, prohibited/required
patterns, format rules, section names, and changelog format.

## Step 4: Choose the contribution method

### Direct page edit (for read-write + direct-push)

1. Edit the page in the library's Wiki/ directory
2. Update ` + "`last_updated`" + ` in frontmatter
3. Rebuild indexes: ` + "`lore library index <library>`" + `
4. Commit and push

### PR workflow (for pr-required)

1. Edit the page, create a branch, commit, open a PR

### Publish from vault

` + "```bash" + `
lore library publish <vault-file> --to <library>
` + "```" + `

### Drop raw evidence

Write a markdown file to ` + "`<library>/sources/incoming/`" + ` for later synthesis.

## Step 5: Verify

` + "```bash" + `
lore library lint <library>         # check formatting
lore library lint <library> --fix   # auto-fix if needed
lore library index <library>        # rebuild indexes
` + "```" + `

## Publishing Policies

| Policy | How to contribute |
|---|---|
| ` + "`direct-push`" + ` | Edit, commit, push to main |
| ` + "`pr-required`" + ` | Branch, commit, open PR |

Check: ` + "`grep publishing <library-path>/library.yaml`" + `
`
}

func generateCaptureCommand() string {
	return `---
description: summarize this session's work and append it to today's daily log
---
Summarize what was accomplished in this session and append it to the vault's daily log.
This command is idempotent — it reads the existing log and only adds new bullets for work
not already captured.

## 1. Determine the Vault

Find the vault path using the LORE_VAULT environment variable or by locating a .lore/config.yaml.
If the vault cannot be found, ask the user.

## 2. Read Existing Daily Log

Read today's daily log file at ` + "`<vault>/Daily Log/YYYY-MM/YYYY-MM-DD.md`" + ` (if it exists).
Note all existing bullet points — these represent work already captured (possibly from
an earlier /capture in this same session or from manual ` + "`lore note`" + ` calls).

## 3. Reflect on the Session

Review the conversation history. Identify:
- What tasks were worked on (features, bugs, refactors, investigations)
- What decisions were made and why
- What files were created or modified
- What's still in progress or blocked
- Any entities (services, people, environments) that were discussed

## 4. Deduplicate

Compare your candidate bullets against the existing daily log content.
**Do not re-add work that is already captured** — even if worded differently.
If an existing bullet covers the same work but is less complete, you may add a
follow-up bullet that extends it (e.g., "— completed" or additional detail),
but do not repeat the original.

## 5. Format the Notes

For each NEW piece of work (not already in the log), produce a bullet suitable for ` + "`lore note`" + `.
Each bullet should be a concise, factual statement of what happened — not a full paragraph.

Good bullets:
- "refactored auth middleware to support token refresh"
- "fixed race condition in daemon watcher — fsnotify debounce was too short"
- "investigated prod latency spike — root cause: connection pool exhaustion in gateway"

Bad bullets:
- "worked on stuff" (too vague)
- "I spent time looking at the code and then made some changes to improve it" (too wordy)

## 6. Detect Tags

If a bullet clearly relates to a known entity type, add a tag:
- Code/repo work: no tag needed (default)
- Planning/process: #planning
- Incident/debugging: #incident
- Documentation: #docs

Only tag when it adds signal. Most bullets need no tag.

## 7. Append to Daily Log

If there are new bullets to add, run for each:

` + "```bash" + `
lore note "<bullet text>" [--tag "#tag"]
` + "```" + `

Run each command separately so errors are visible.

If there is nothing new to capture (all work already logged), say so and skip.

## 8. Confirm

Print the path to today's daily log file, how many bullets were added, and how many
were skipped as duplicates.
`
}
