# AI agents

Flock includes an [MCP](https://modelcontextprotocol.io) server, so AI agents like Claude Code, Cursor, VS Code (Copilot), Windsurf and Codex can work with your sites: look up their URLs and PHP versions, read the Laravel log, dumps and caught mail, and install the PHP versions and services a project needs.

## Setting it up

You add Flock to your AI clients once, and it works in every project: Flock finds the site from the folder the agent works in.

:::app
Open **Settings → AI** and click **Add** next to each client you use. Then restart the client, or reload its MCP servers. **Remove** takes Flock out of a client again.

For a client that isn't listed, copy the configuration under **Other clients** into its MCP settings.
:::

:::cli
```bash
flock mcp install
```

This registers Flock with every AI client it finds on your computer, after asking. To pick one, pass `--client` with `claude-code`, `cursor`, `vscode`, `windsurf` or `codex`. Restart the client, or reload its MCP servers, afterwards.

`flock mcp status` shows which clients Flock is registered with, and `flock mcp uninstall` removes it again.
:::

Flock only adds its own `flock` entry to a client's settings and leaves everything else as it is. If it can't read a settings file (for example a VS Code `mcp.json` with comments in it), it tells you, and you add the entry yourself:

```json
{
  "mcpServers": {
    "flock": {
      "command": "/Users/you/Library/Application Support/Flock/bin/flock",
      "args": ["mcp"]
    }
  }
}
```

On Windows the command is `C:\Users\you\AppData\Roaming\Flock\bin\flock.exe`, and on Linux `/home/you/.local/share/flock/bin/flock`. VS Code calls the top-level key `servers` instead of `mcpServers`, and its entry also has `"type": "stdio"`. Codex doesn't use JSON: its entry is a `[mcp_servers.flock]` table in `~/.codex/config.toml`.

## What the agent can do

When it connects, the agent learns which site it's working on, its URL and its PHP and Node.js versions. It also learns that Flock always serves the site, so it shouldn't run `php artisan serve`, and that `php`, `composer` and `npm` already use the project's versions.

These tools only read, so most clients run them without asking:

| Tool | What it does |
| --- | --- |
| `status` | Whether Flock runs, the default PHP and Node.js versions, and any problems |
| `site_info` | A site's URLs, folder, HTTPS, PHP and Node.js versions (and where they come from), its server, and its Reverb server with the `.env` lines it needs |
| `php_extensions` | The PHP extensions a site requires or uses, and whether Flock's PHP has each |
| `list_sites` | Every site with its URL, folder and PHP version |
| `list_worktrees` | The [git worktrees](worktrees.md) Flock serves: their branch, URL, folder, setup and database |
| `list_services` | Databases and other services: installed, running, their port and the `.env` settings to connect |
| `list_php_versions` | Installed and available PHP versions |
| `list_node_versions` | Installed and available Node.js versions |
| `get_errors` | Recent errors from the site's Laravel log and from PHP |
| `get_dumps` | Captured `dump()` and `dd()` output, with the file and request that produced it |
| `get_events` | The queries, logs, views, jobs and requests a page or command ran, grouped by request |
| `list_mail` | Mail your apps sent, caught by Flock |
| `read_mail` | One email: its text and links (for example to verify a sign-up), and its attachments |
| `service_logs` | The last lines of a service's log, e.g. MySQL's |

These tools change things, so clients ask you first:

| Tool | What it does |
| --- | --- |
| `service` | Install, start or stop a service |
| `install_version` | Install a PHP or Node.js version |
| `link_site` | Serve a project as a new site |
| `proxy_site` | Proxy `<name>.test` to a local service, e.g. a dashboard or a Docker container, optionally with the command that starts its server |
| `proxy_server` | Start or stop the server of a proxy that has a command |
| `update_site` | Turn HTTPS on or off, isolate, unisolate, pin or unpin a version, add a domain, switch between FrankenPHP (with or without Octane) and PHP-FPM, serve it as a PHP site or a Node.js app, run, stop or restart its Reverb server, or restart or stop it |
| `set_debugging` | Turn Xdebug or dump capture on or off |
| `create_database` | Create a database in PostgreSQL, MySQL or MariaDB |
| `create_worktree` | Create a [git worktree](worktrees.md) of a site on a branch (or one Flock names), at its own URL, and wait while Flock sets it up |
| `setup_worktree` | Set a worktree up, or wait for its setup |
| `postgres_extension` | List or install PostGIS, pgRouting, pgvector or TimescaleDB for a PostgreSQL version, and tell the agent the `CREATE EXTENSION` to run |

None of the tools remove anything: an agent can't unlink sites, uninstall versions or services, remove worktrees, drop databases, or delete data, mail or logs.

Flock also offers a `debug_site` prompt, which gathers the site's setup, recent errors, dumps, the latest request's events and mail and asks the agent to find what's wrong, a `site_information` resource with the current site's details, and `flock://sites/{name}` resources with the details of any site.

## Which site the agent works on

Flock uses the folder the agent's client starts it in. When that isn't inside a site, or the agent works on another one, the agent passes the site's name or folder to the tool. Started in a git worktree, the agent works on the worktree, and Flock tells it which site it belongs to. To fix the site for a client, start the server with `flock mcp --site <name>`, or set `FLOCK_SITE`.

## What the agent can see

The agent reads the same things you see in Flock: your sites' logs, captured dumps and caught mail. Like any file an agent reads, what it reads becomes part of your conversation with the AI provider.

## Laravel Boost

Flock works alongside [Laravel Boost](https://boost.laravel.com): you install Flock's MCP server once for all your projects, and Boost's per project.

Boost also writes guidelines for the agents into `CLAUDE.md`, `AGENTS.md` and the other agent files. In a project that uses Boost, you can add Flock's guideline to them. It tells agents that Flock serves the app and picks its PHP version.

:::app
In **Settings → Sites**, projects that use Boost show **Add Flock to its guidelines…** under their name, or **Update it…** when the guideline is outdated.
:::

:::cli
```bash
cd ~/Code/acme
flock boost
```
:::

This writes `.ai/guidelines/flock.md` and runs `php artisan boost:update`, so Boost adds it to the agent files. Commit it with the rest of `.ai/guidelines`. The guideline says nothing specific to your computer, so it's right for everyone on the project, including teammates who don't use Flock.
