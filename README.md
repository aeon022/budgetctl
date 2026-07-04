# budgetctl

Terminal budget tracker. Import bank CSV exports, categorize transactions, set spending goals, and detect subscriptions — all from the command line or a Bubbletea TUI.

Part of the [missionctl](https://github.com/aeon022/missionctl) suite. No bank API. No cloud. Works from CSV exports you download yourself.

---

## Quick Start

**1. Install**

```bash
git clone https://github.com/aeon022/budgetctl && cd budgetctl
./setup.sh
```

**2. Import your bank export**

```bash
budgetctl import ~/Downloads/n26-export.csv --account checking
```

**3. Tag transactions with categories**

```bash
budgetctl tag "Netflix" --category streaming --apply
budgetctl tag "REWE" --category groceries --apply
```

**4. View transactions and summaries**

```bash
budgetctl          # open TUI (list view)
budgetctl summary  # monthly income / expenses / net
```

**5. Set budget goals**

```bash
budgetctl goal set groceries 300
budgetctl goal set dining 150
budgetctl goal list
```

---

## Cheatsheet

```
budgetctl [tui]                                      Open TUI (default)
budgetctl import FILE [--account NAME]               Import bank CSV
budgetctl list [--month 2026-07] [--category NAME] [--query TEXT] [--limit N] [--json]
budgetctl summary [--month 2026-07] [--json]         Monthly summary
budgetctl tag PATTERN --category NAME [--apply]      Create category rule
budgetctl apply-rules                                Re-apply all rules
budgetctl goal set CATEGORY AMOUNT                   Set monthly limit
budgetctl goal list [--month 2026-07]                Show goal progress
budgetctl goal delete CATEGORY                       Remove a goal
budgetctl recurring                                  Detect recurring payments
budgetctl export [--year 2026] [--format csv|json] [-o FILE]
budgetctl mcp                                        Start MCP server (stdio)
```

---

## CLI Reference

### Import and List

| Command | Description |
|---|---|
| `budgetctl import FILE` | Import a bank CSV export. Auto-detects N26, ING, DKB, or generic format. |
| `budgetctl import FILE --account NAME` | Tag imported transactions with an account name. |
| `budgetctl list` | List transactions for the current month. |
| `budgetctl list --month 2026-07` | List transactions for a specific month. |
| `budgetctl list --category groceries` | Filter by category. |
| `budgetctl list --query "netflix"` | Full-text search across descriptions. |
| `budgetctl list --limit 50` | Limit number of results. |
| `budgetctl list --json` | Output as JSON for scripting. |
| `budgetctl summary` | Show income, expenses, net, and category breakdown. |
| `budgetctl summary --month 2026-06` | Summary for a specific month. |
| `budgetctl summary --json` | Machine-readable summary output. |

### Categorization

| Command | Description |
|---|---|
| `budgetctl tag PATTERN --category NAME` | Create a rule: any transaction whose description contains PATTERN (case-insensitive) gets assigned NAME. |
| `budgetctl tag PATTERN --category NAME --apply` | Create the rule and immediately apply it to all existing transactions. |
| `budgetctl apply-rules` | Re-run all category rules against all transactions in the database. Useful after importing new data without `--apply`. |

Rules use substring matching. Examples:

```bash
budgetctl tag "Netflix" --category streaming
budgetctl tag "Spotify" --category streaming
budgetctl tag "REWE" --category groceries --apply
budgetctl tag "Miete" --category rent --apply
budgetctl apply-rules
```

### Goals

| Command | Description |
|---|---|
| `budgetctl goal set CATEGORY AMOUNT` | Set a monthly spending limit (in EUR) for a category. |
| `budgetctl goal list` | Show all goals with current-month progress bars. |
| `budgetctl goal list --month 2026-06` | Show goal progress for a past month. |
| `budgetctl goal delete CATEGORY` | Remove a goal. |

```bash
budgetctl goal set groceries 300
budgetctl goal set dining 150
budgetctl goal set streaming 25
budgetctl goal list
```

Progress is shown as a bar colored green (under 80%), amber (80–99%), or red (at or over 100%).

### Recurring Payments

```bash
budgetctl recurring
```

Scans all transactions, groups by normalized description and amount, and detects monthly, weekly, and annual patterns using median gap analysis. Use it to surface subscriptions, rent, utilities, and loan payments you may have forgotten.

### Export

| Command | Description |
|---|---|
| `budgetctl export` | Export all transactions as CSV to stdout. |
| `budgetctl export --year 2026` | Export transactions for a specific year. |
| `budgetctl export --format json` | Export as JSON instead of CSV. |
| `budgetctl export -o FILE` | Write output to a file instead of stdout. |

```bash
budgetctl export --year 2026 -o 2026-taxes.csv
budgetctl export --year 2026 --format json | jq '.[] | select(.category == "business")'
```

### MCP Server

```bash
budgetctl mcp
```

Starts an MCP (Model Context Protocol) server over stdio. Exposes all budget data and operations as tools that an AI assistant can call. See [MCP — AI Integration](#mcp--ai-integration) below.

---

## Supported CSV Formats

| Bank | Language | Key Columns | Notes |
|---|---|---|---|
| N26 | German | `Datum`, `Betrag EUR` | Standard N26 account export |
| ING | German | `Buchung`, `Betrag` | Semicolon-delimited |
| DKB | German | `Wertstellung`, `Gläubiger-ID` | Deutsche Kreditbank format |
| Generic | Any | Auto-detected | Looks for date, amount, and description columns by header name |

For generic CSVs, budgetctl scans the header row and picks the most likely columns. If detection fails, rename your headers to `date`, `amount`, and `description`.

---

## TUI Guide

Launch the TUI with `budgetctl` or `budgetctl tui`.

### List View (default)

Displays a scrollable transaction list for the current month. Columns: date, description, category, amount.

- Date color: today = amber, this week = muted, older = subtle
- Amount color: green = income, red = expense
- Category column shows the assigned category (blank if uncategorized)

### Summary View (press `s`)

Shows a full-month overview:

- Income / expenses / net totals at the top
- Category bar chart showing spend per category
- Budget goals section with color-coded progress bars (green < 80%, amber 80–99%, red >= 100%)

### Keybindings

**List View**

| Key | Action |
|---|---|
| `j` / `k` | Navigate down / up |
| `PgDn` / `PgUp` | Page down / up |
| `g` / `G` | Jump to first / last transaction |
| `s` | Switch to Summary view |
| `S` | Sync (re-apply rules, refresh data) |
| `/` | Open search |
| `Tab` / `Shift+Tab` | Next / previous month |
| `Enter` / `Esc` | Back / cancel |
| `q` | Quit |

**Summary View**

| Key | Action |
|---|---|
| `Esc` | Back to List view |
| `Tab` / `Shift+Tab` | Next / previous month |
| `↑` / `↓` | Scroll |
| `q` | Quit |

---

## MCP — AI Integration

budgetctl can act as an MCP server, letting Claude Desktop (or any MCP-compatible AI client) query and manage your budget data directly.

### Claude Desktop Configuration

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "budgetctl": {
      "command": "budgetctl",
      "args": ["mcp"]
    }
  }
}
```

Restart Claude Desktop. budgetctl tools will appear automatically.

### MCP Tools

| Tool | Description |
|---|---|
| `list_transactions` | List transactions, filtered by month, category, or query string |
| `budget_summary` | Monthly income, expenses, and net with full category breakdown |
| `import_transactions` | Import a CSV file by path |
| `tag_transactions` | Create a category rule (pattern, category, and optionally apply immediately) |
| `apply_category_rules` | Re-apply all existing rules to all transactions |
| `list_budget_goals` | List all goals with current-month spend and progress |
| `set_budget_goal` | Set a monthly spending limit for a category |
| `delete_budget_goal` | Remove a goal by category name |
| `detect_recurring_payments` | Scan all transactions for subscription and recurring payment patterns |

### AI Workflow Examples

**Monthly spending analysis**

Ask Claude: "Give me a breakdown of my spending last month and compare it to the month before."

Claude will call `budget_summary` for both months and synthesize the comparison, highlighting categories where spending increased or decreased significantly.

**Set up budget goals via AI**

Ask Claude: "Look at my last three months of spending and suggest realistic budget goals for each category."

Claude will call `budget_summary` for each month, compute averages, and then call `set_budget_goal` for each category with a suggested limit — explaining its reasoning before making changes.

**Find subscriptions to cancel**

Ask Claude: "Find all my recurring payments and identify any I might want to cancel."

Claude will call `detect_recurring_payments`, then `list_transactions` to cross-reference amounts, and present a ranked list of subscriptions by monthly cost with a cancellation recommendation for ones that look unused or duplicated.

---

## Architecture

```
Bank website  →  CSV export  →  budgetctl import  →  SQLite (~/.local/share/budgetctl/budget.db)
                                                            |
                                          ┌─────────────────┼─────────────────┐
                                       TUI (Bubbletea)   CLI (Cobra)    MCP server (stdio)
```

Data lives entirely on your machine. No network calls are made outside of MCP tool invocations (which run only when an AI client connects). The SQLite database is a single file you can inspect, back up, or delete at any time.

**Requirements:** macOS or Linux, Go 1.21+
