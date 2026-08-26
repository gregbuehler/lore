# lore Command Reference

Generated from the Cobra command tree. Run `lore docs commands docs/commands.md` to refresh.

### `lore`

Institutional knowledge on your filesystem

### `lore agent`

Manage local agent configuration

### `lore agent local <provider|status>`

Write or inspect machine-local agent preferences

Flags:

- `--force`: Overwrite existing .lore/local.yaml

### `lore context <node>`

Assemble context for an entity

Flags:

- `--brief`: Truncate page content to first section
- `--depth`: Graph traversal depth for relationships
- `--vault`: Path to vault (auto-detected if not set)

### `lore daemon`

Manage the lore index daemon

### `lore daemon install`

Install the lore daemon as a system auto-start service

Flags:

- `--vault`: Path to vault (overrides LORE_VAULT and auto-detection)

### `lore daemon reindex`

Force a full reindex

### `lore daemon start`

Start the daemon (foreground)

Flags:

- `--vault`: Path to vault (auto-detected if omitted)

### `lore daemon status`

Show daemon status and index stats

### `lore daemon stop`

Stop the running daemon

### `lore daemon uninstall`

Uninstall the lore daemon auto-start service

### `lore discover`

Discover available libraries from registries

### `lore docs`

Generate project documentation

### `lore docs commands [output-path]`

Generate the command reference from the CLI tree

Flags:

- `--help`, `-h`: help for commands

### `lore doctor`

Check the search index for damage

Flags:

- `--repair`: Rebuild the FTS index if verification fails

### `lore entity`

CRUD commands for Wiki entity pages

### `lore entity create <path>`

Create a new Wiki entity page

Flags:

- `--title`: Entity title (defaults to basename of path)
- `--type`: Entity type (required): service, environment, person, tool, infrastructure, organization, customer, vendor, concept
- `--vault`: Path to vault (auto-detected if omitted)

### `lore entity delete <path>`

Delete a Wiki entity page

Flags:

- `--confirm`: Confirm deletion (required)
- `--force`: Delete even when backlinks cannot be checked
- `--vault`: Path to vault (auto-detected if omitted)

### `lore entity get <path>`

Print an entity page

Flags:

- `--full`: Print entire file content
- `--json`: Output structured JSON (frontmatter + relationships)
- `--vault`: Path to vault (auto-detected if omitted)

### `lore entity list`

List Wiki entity pages

Flags:

- `--json`: Output as JSON
- `--type`: Filter by entity_type
- `--vault`: Path to vault (auto-detected if omitted)

### `lore entity update <path>`

Update frontmatter or append to sections of an entity page

Flags:

- `--append-changelog`: Append a dated entry to the ## Change Log section
- `--append-section`: Append text to a named section: "Section Name:text" (repeatable)
- `--set`: Set a frontmatter field: key=value (repeatable)
- `--vault`: Path to vault (auto-detected if omitted)

### `lore fix-links`

Rewrite stale wikilinks in markdown files

Flags:

- `--dry-run`: Preview changes without writing files

### `lore help [command]`

Help about any command

### `lore library`

Library administration commands

### `lore library index [library]`

Rebuild library and meta indexes

### `lore library init <path>`

Initialize a new shared library

### `lore library lint [library]`

Check library page health

Flags:

- `--all`: Lint all subscribed libraries
- `--fix`: Auto-fix formatting issues

### `lore library maintain <library>`

Synthesize new evidence into library pages

Flags:

- `--agent`: Agent provider for this run: claude, codex, custom, or none
- `--dangerously-skip-permissions`: Pass --dangerously-skip-permissions to the configured agent
- `--dry-run`: Generate context packages without invoking the agent
- `--entity`: Maintain a single entity (e.g., --entity storage)

### `lore library register`

Register a library in the registry

### `lore library review <library>`

Show vault learnings not yet in a library

### `lore library seed <source-dir> <library-name>`

Seed library pages from vault content

Flags:

- `--dry-run`: Preview without writing files

### `lore library skills [library]`

List or read library skills

### `lore library watch <library>`

Update library pages from source repo changes

Flags:

- `--agent`: Agent provider for this run: claude, codex, custom, or none
- `--dangerously-skip-permissions`: Pass --dangerously-skip-permissions to the configured agent
- `--dry-run`: Generate context packages without invoking the agent
- `--entity`: Watch a single entity (e.g., --entity argus)

### `lore note <text>`

Append a quick note to today's daily log

Flags:

- `--tag`: Tag prefix to prepend to the note line (e.g. #admin/hiring)
- `--vault`: Path to vault (auto-detected if omitted)

### `lore publish [library-name]`

Commit and push changes from subscribed libraries back to their git repos

Flags:

- `--all`: Stage all repository changes with git add -A
- `--dry-run`: Show what would be published without staging, committing, or pushing
- `--message`, `-m`: Commit message
- `--vault`: Path to vault (auto-detected if omitted)
- `--yes`, `-y`: Publish without interactive confirmation

### `lore query <search terms>`

Search the vault index

Flags:

- `--backlinks`: Show incoming edges to a node
- `--depth`: Graph traversal depth
- `--graph`: Show outgoing edges from a node
- `--json`: Output results as JSON
- `--limit`, `-n`: Max results
- `--type`: Filter by entity_type (or edge_type for graph)

### `lore search <query>`

Raw grep-style markdown search

### `lore subscribe <repo-url | path>`

Subscribe to a shared library

Flags:

- `--local`: Subscribe to a local directory instead of cloning a git repo
- `--name`: Local alias for the library (default: derived from repo/path)
- `--root`: Root directory within the subscription to index

### `lore sync`

Pull latest changes for all subscribed libraries and reindex

Flags:

- `--vault`: Path to vault (auto-detected if omitted)

### `lore thread`

Thread management commands

### `lore thread new <topic>`

Scaffold a new investigation thread

Flags:

- `--related`: Comma-separated list of related entities to link (e.g. Wiki/Services/gateway,Wiki/Environments/production)
- `--status`: Thread status (default: active)
- `--vault`: Path to vault (auto-detected if omitted)

### `lore ui`

Launch the interactive TUI

### `lore unsubscribe <name>`

Unsubscribe from a shared library

### `lore update [name]`

Pull latest changes for subscribed libraries

### `lore vault`

Vault management commands

### `lore vault context`

Generate .lore/LORE.md and wire it into agent instructions

Flags:

- `--agent`: Agent context file to wire: auto, claude, codex, all, or none

### `lore vault init [path]`

Initialize a new lore vault

Flags:

- `--adopt`: Adopt an existing directory as a lore vault
- `--agent`: Agent scaffold to create: claude, codex, or none
- `--email`: Your email (for config)
- `--entities`: Entity types, comma-separated (e.g., people,services,tooling)
- `--host`: Git forge host (GHE, GitLab, etc). Auto-detected from gh CLI if available
- `--name`: Your name (for config and templates)

### `lore vault lint`

Check vault page health

Flags:

- `--fix`: Auto-fix missing frontmatter

### `lore vault status`

Show vault and library status
