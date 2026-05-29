# lore

Institutional knowledge on your filesystem.

`lore` manages personal knowledge vaults and shared team libraries backed by markdown and git. It handles the plumbing — subscriptions, publishing, search, graph traversal, and agent-driven maintenance — so your editor and your LLM agent can focus on the content.

A persistent daemon indexes your vault and libraries into SQLite (FTS5 for BM25-ranked search, typed graph edges for relationship traversal). Query from the CLI, a terminal UI, or let your agent use it as a knowledge backend.

## Install

```bash
go install github.com/gbuehler/lore@latest
```

Or build from source:

```bash
git clone https://github.com/gbuehler/lore.git
cd lore
go install ./...
```

## Quick Start

### 1. Create a vault

```bash
lore vault init ~/vault
cd ~/vault
```

This creates a personal vault with `.lore/config.yaml` and the default directory structure. If you already have an Obsidian vault or markdown directory:

```bash
lore vault init --adopt ~/my-existing-vault
```

### 2. Subscribe to a library

```bash
lore subscribe git@git.example.com:team/services.git
```

Or subscribe to a local directory:

```bash
lore subscribe local:/path/to/library
```

### 3. Start the daemon

```bash
export LORE_VAULT=~/vault
lore daemon start
```

The daemon watches your vault and libraries, maintains a SQLite FTS5 index, and serves queries over a Unix socket. It auto-starts on first query if `LORE_VAULT` is set.

To auto-start at login:

```bash
lore daemon install   # macOS launchd or Linux systemd --user
```

### 4. Search and explore

```bash
lore query "gateway mTLS"                    # BM25 full-text search
lore query --graph Wiki/Services/gateway     # outgoing graph edges
lore query --backlinks Wiki/Services/gateway # incoming references
lore context Wiki/Services/gateway --brief   # assembled LLM context
lore ui                                     # terminal UI
```

### 5. Generate agent context

```bash
lore vault context
```

This writes `.lore/LORE.md` with your library subscriptions, skills, and workflows, and imports it into `.claude/CLAUDE.md` so Claude Code sees everything on session start.

For Codex-based vaults, the same generated `.lore/LORE.md` is imported from `AGENTS.md`.

### 6. Explore further

```bash
lore vault status              # see what you're subscribed to
lore search "authx mTLS"       # legacy search (pre-daemon)
lore library skills             # list available skills
```

## Concepts

**Vaults** are personal. Your notes, daily logs, drafts, and context. A vault is a directory of markdown files with `.lore/config.yaml`. Your agent works here.

**Libraries** are shared. Team knowledge bases that anyone can subscribe to. Each library is a git repo (or local directory) containing Wiki pages, skills, and a `library.yaml` schema. Subscribing clones it locally; your agent reads it as local markdown alongside your vault.

**Skills** are procedural knowledge curated in libraries. They encode retrieval paths — not facts, but *how to find out*. When a library has a skill like `deployed-versions`, any agent with access to that library knows how to answer "what versions are deployed on staging?" without re-deriving the answer path.

**Excerpts** are self-descriptions that libraries publish. Each library generates an `excerpt.md` summarizing its pages, skills, and sources. `lore vault context` assembles these excerpts into your vault's agent context — so libraries own their own descriptions and the vault doesn't need to parse library internals.

## Commands

### Vault

```
lore vault init [path]         Create a new vault (--adopt for existing dirs)
lore vault status              Show vault and library status
lore vault lint [--fix]        Check vault page health
lore vault context             Generate .lore/LORE.md agent context
```

### Libraries

```
lore library init <path>       Scaffold a new shared library
lore library index [name]      Rebuild Wiki/index.md + excerpt.md
lore library lint <name>       Check library page health (--fix, --all)
lore library skills [name]     List or read library skills
lore library seed <dir> <lib>  Bulk import vault pages into a library
lore library publish <file>    Publish a single file to a library
lore library review <name>     Surface daily log evidence not yet in library
lore library maintain <name>   Synthesize daily log evidence into pages
lore library watch <name>      Update pages from source repo changes
lore library register          Register a library in the registry
```

### Subscriptions

```
lore subscribe <repo|path>     Subscribe to a library
lore unsubscribe <name>        Unsubscribe from a library
lore update [name]             Pull latest for all or one library
lore sync                      Git-pull all libraries + trigger reindex
lore search <query>            Search across vault + libraries
lore discover                  List available libraries from registries
```

### Daemon & Search

```
lore daemon start              Start the index daemon
lore daemon stop               Stop it
lore daemon status             Check if running + index stats
lore daemon reindex            Force full reindex
lore daemon install            Install as login service (launchd/systemd)
lore daemon uninstall          Remove the login service
lore query <terms>             BM25 full-text search
lore query --graph <node>      Outgoing graph edges from a node
lore query --backlinks <node>  Incoming edges to a node
lore context <node>            Assembled context (page + edges + mentions)
lore ui                        Launch terminal UI
```

### Content

```
lore entity create <type> <name>   Create a new Wiki entity page
lore entity update <path>          Update entity frontmatter
lore entity get <path>             Print entity details
lore entity delete <path>          Delete an entity page
lore entity list [--type <t>]      List entities
lore note <text>                   Append to today's daily log
lore thread new <topic>            Scaffold an investigation thread
lore fix-links [--dry-run]         Resolve broken wikilinks across vault
lore publish [library]             Commit+push pending library changes
```

## Daemon Architecture

The lore daemon is a lightweight background process that provides fast search and graph queries:

```
┌─────────────┐     Unix socket      ┌──────────────────────┐
│  lore query │ ───────────────────── │     lore daemon      │
│  lore ui    │  length-prefixed JSON │                      │
│  agent      │                       │  fsnotify watcher    │
└─────────────┘                       │  SQLite FTS5 index   │
                                      │  typed graph edges   │
                                      └──────────────────────┘
```

- **Index**: SQLite with FTS5 virtual table for BM25-ranked full-text search
- **Graph**: `edges` table storing typed relationships (owner, depends_on, deployed_in, mentions) extracted from wikilinks in section context
- **Watcher**: fsnotify-based recursive file watching with 500ms debounce
- **Resolver**: Obsidian-style shortest-path wikilink resolution (folder expansion, proximity ranking, ancestor walking)
- **Protocol**: Length-prefixed JSON over Unix socket (`~/.lore/daemon.sock`)

The daemon auto-starts on first query when `LORE_VAULT` is set. All CLI commands fall back to direct SQLite access if the daemon is unavailable.

## Library Anatomy

```
my-library/
  library.yaml           # schema, tone rules, TTLs, sources, skills
  CLAUDE.md              # agent instructions for this library
  excerpt.md             # self-description (generated by lore library index)
  Wiki/
    index.md             # navigation index (generated)
    Services/            # entity pages grouped by type
      gateway.md
      storage.md
  skills/
    deployed-versions.md # procedural knowledge
  sources/
    incoming/            # contributor drop zone
  log.md                 # activity log
```

### library.yaml

```yaml
name: "services"
description: "Platform service knowledge"

publishing: pr-required

tone:
  voice: third-person
  prohibited:
    - personal characterizations
  required:
    - factual sourcing

default_ttl:
  service: 90d
  environment-config: 14d

skills:
  - name: deployed-versions
    file: skills/deployed-versions.md
    description: Look up deployed software versions

sources:
  - repo: git.example.com/myorg/deployment
    local: ~/src/git.example.com/myorg/deployment
    watch:
      - path: deployments/{entity}/**
        maps_to: environment
```

### Skills

A skill is a markdown file in `skills/` with frontmatter and a step-by-step procedure:

```yaml
---
name: deployed-versions
description: Look up deployed software versions
trigger: "what versions are deployed on {environment}"
inputs:
  - name: environment
    required: true
---

# Deployed Versions

## Fast Path: Service Catalog
...

## Detailed Path: Source Repos
...
```

Skills teach agents *how to answer questions*, not *what the answer is*. The library's CLAUDE.md references skills so agents discover them automatically.

## Maintenance Pipeline

lore's maintenance system turns raw evidence into synthesized library knowledge:

```
Daily Logs ──→ lore library review ──→ surface new evidence
             → lore library maintain ──→ agent synthesizes into pages

Source Repos ──→ lore library watch ──→ agent updates pages from IaC changes

Sync ──→ lore sync ──→ pull all libraries + reindex
Push ──→ lore publish ──→ commit+push pending changes to library repos
```

### How `maintain` works

1. Scans daily log files for entity mentions newer than each page's `last_updated`
2. Matches mentions using entity names and aliases (word-boundary-aware)
3. Assembles a context package: current page + new evidence + tone rules + format rules
4. Invokes the configured agent to rewrite the page
5. Cleans up the context package and rebuilds indexes

### How `watch` works

1. Reads `sources:` from `library.yaml` — repos and path patterns to track
2. Maps entities to repo directories using `{entity}` placeholders and inventory data
3. Runs `git log --since=<last_updated>` to find relevant commits
4. Assembles a context package with commits, authors, file lists, and instructions
5. Invokes the agent to synthesize changes into the page

### Agent providers

Maintenance commands use `agent.provider` to decide how agent work is executed. Shared defaults live in `.lore/config.yaml`; machine-local preferences can live in `.lore/local.yaml`, so the same synced vault can use Codex at home and Claude at work without editing the shared config.

| Provider | Behavior |
|----------|----------|
| `claude` | Run Claude non-interactively with `claude -p <prompt>`. |
| `codex` | Run Codex non-interactively with `codex exec`, send the prompt on stdin, and pass `--cd <workdir>`. Sandbox and approval settings are configurable. |
| `custom` | Run a user-supplied command for sites that wrap or proxy an agent. |
| `none` | Do not invoke an agent; commands stop before synthesis. |

Dangerous permission bypass is always opt-in. When enabled, lore maps it to the provider-specific dangerous flag, such as Claude's `--dangerously-skip-permissions` or Codex's equivalent unsafe bypass flag. Leave it disabled for normal local or CI maintenance.

Agent selection precedence is:

1. Command flag, for example `lore maintain services --agent codex`
2. Environment, for example `LORE_AGENT_PROVIDER=codex`
3. Machine-local `.lore/local.yaml`
4. Shared `.lore/config.yaml`
5. Backward-compatible default: Claude

Example `.lore/local.yaml` for a Codex machine:

```yaml
agent:
  provider: codex
  command: codex
  sandbox: workspace-write
  approval: never
```

Generate those local overrides with:

```bash
lore agent local codex
lore agent local claude
lore agent local none
lore agent local status
```

### CI Automation

When a library is a git repo, maintenance runs as GitHub Actions:

```yaml
on:
  schedule:
    - cron: '0 6 * * 1'
  workflow_dispatch:
jobs:
  maintain:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: lore library watch my-library
      - run: lore library lint my-library --fix
      - run: lore library index my-library
      - uses: peter-evans/create-pull-request@v6
```

## Linting

`lore vault lint` checks vault pages. `lore library lint` checks library pages with additional rules:

| Check | Vault | Library | Fixable |
|-------|-------|---------|---------|
| Missing frontmatter | yes | yes | yes |
| Empty files | yes | yes | no |
| Missing entity_type | | yes | no |
| Stale pages (TTL) | | yes | no |
| Stale index | | yes | no |
| Local filesystem paths | | yes | yes |
| Section names | | yes | yes |
| Change log format | | yes | yes |
| Required sections | | yes | no |
| Frontmatter field order | | yes | no |

## Agent Context

lore generates context so your agent understands the knowledge system:

```
.lore/LORE.md             ← generated library/skills discovery

.claude/CLAUDE.md         ← Claude vault operating manual
  @../.lore/LORE.md       ← import: generated context

AGENTS.md                 ← Codex vault operating manual
  @.lore/LORE.md          ← import: generated context

Library CLAUDE.md          ← per-library agent instructions
  excerpt.md               ← per-library self-description
```

Run `lore vault context` after subscribing to new libraries or adding skills. It regenerates `.lore/LORE.md` and wires the import into the active provider's context file idempotently: `.claude/CLAUDE.md` for Claude and `AGENTS.md` for Codex.
