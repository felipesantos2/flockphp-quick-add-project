# CLI reference

Almost every feature of Flock is available from the `flock` command. Run `flock --help` or `flock <command> --help` for details. `flk` is a shorter name for the same command: `flk link` does what `flock link` does.

On Linux, the package puts only `flk` on your PATH, because `/usr/bin/flock` is a file-locking tool that comes with every Linux system: run `flk install` to set Flock up. After that, `flock` works too, and hands every command that isn't Flock's to the system's `flock`, so scripts that use it keep working.

:::app
This page lists the `flock` commands. The other pages show where to find each action in the app.
:::

## Setup

| Command | Description |
| --- | --- |
| `flock install [--skip-privileged]` | Set Flock up: shims on PATH, background daemon, `.test` DNS and trusted HTTPS |
| `flock uninstall [--purge]` | Remove Flock. Keeps site settings and databases unless `--purge` is given |
| `flock status` | Show the state of the daemon and its services |
| `flock restart` | Restart the daemon and all services |
| `flock logs [name] [-n 50] [-f]` | Show service logs (daemon, web, php8.5-fpm on macOS and Linux or php8.5-cgi on Windows, postgres, …). Without a name, lists the logs |
| `flock update` | Update Flock, CLI and daemon included: the app installs the new version (on Linux, apt or dnf) |
| `flock version` | Print the Flock version |

## Sites

| Command | Description |
| --- | --- |
| `flock app create <name> [--type <template>] [--db] [--db-service <service>] [--php 8.4] [--secure]` | Create a project from a template in a new subdirectory and link it as `<name>.test` |
| `flock park [dir]` | Serve every subdirectory of `dir` as `<subdir>.test` |
| `flock unpark [dir]` | Stop serving a parked directory (alias: `forget`) |
| `flock link [name] [--php 8.4] [--secure]` | Serve the current directory as `<name>.test` |
| `flock unlink [name]` | Remove a linked site |
| `flock sites` | List linked and parked sites, and proxies |
| `flock proxy <name> <port\|host:port\|url> [--secure]` | Proxy `<name>.test` to a local service, e.g. a Node.js dev server |
| `flock unproxy <name>` | Stop serving a proxy |
| `flock proxies` | List proxies |
| `flock secure [name]` | Serve a site or proxy over HTTPS with a trusted certificate |
| `flock unsecure [name]` | Serve a site or proxy over plain HTTP |
| `flock domain add <domain> [site]` | Add a subdomain or alias to a site (`flock domains` works too) |
| `flock domain remove <domain> [site]` | Remove an extra domain (alias: `rm`) |
| `flock domain list [site]` | List a site's extra domains (alias: `ls`) |
| `flock worktree add [branch] [--from <ref>] [--path <dir>] [--db <mode>]` | Create a git worktree of the current site on a branch (without one, Flock picks a name) and set it up |
| `flock worktree setup [path] [--again] [--db <mode>]` | Set a worktree up, or follow the setup that runs |
| `flock worktree list [--site <name>]` | List the git worktrees Flock serves, of every site or of one |
| `flock worktree open [name]` | Open a worktree in the browser (the one you're in, by default) |
| `flock worktree db <shared\|copy\|fresh> [name]` | Switch a worktree's database |
| `flock worktree remove [name] [--force]` | Run a worktree's `before_delete` hook, remove it with git and drop its own database (alias: `rm`) |
| `flock share [name] [--allow <email>] [--debug]` | Share a site at a temporary public URL until you press <kbd>Ctrl</kbd>+<kbd>C</kbd> |
| `flock share [name] --stop` | Stop sharing a site (e.g. one shared from the app) |

### Creating an application

```sh
flock app create my-app
flock app create my-blog --type wordpress
```

Creates a project in `<name>` from a template (`blank`, `php`, or `wordpress`; default `php`) and links it as `<name>.test`. Add or override templates in `apps.conf` inside Flock's data directory (`%AppData%\Flock` on Windows, `~/.local/share/flock` on Linux, `~/Library/Application Support/Flock` on macOS). Templates marked with `| db` (such as `wordpress`) and `--db` require running MySQL or MariaDB, create a matching database (and fill `wp-config.php` from `wp-config-sample.php`), and `--db-service` selects a specific one. Existing folders, sites, and proxies are never overwritten.

## PHP & Node.js

| Command | Description |
| --- | --- |
| `flock php install <version>` | Install a PHP version, e.g. `8.5` (`--from <file>` for a local build) |
| `flock php list` | List installed PHP versions |
| `flock use <version>` | Set the default PHP version |
| `flock node install <version>` | Install the newest release of a Node.js major, e.g. `22` |
| `flock node list` | List installed Node.js versions |
| `flock node default <major>` | Set the default Node.js version |
| `flock isolate <php\|node> <version> [--site <site>]` | Use a version for this site, on this machine only |
| `flock unisolate <php\|node> [--site <site>]` | Remove an isolation |
| `flock isolated` | List every site's versions and where they come from |
| `flock pin <php\|node> [version] [--site <site>]` | Write a version into the project (`.flockrc` or `.nvmrc`) |
| `flock unpin <php\|node> [--site <site>]` | Remove a pinned version |
| `flock ext` | Show the PHP extensions the current project requires (composer.json, composer.lock), the ones a Laravel app uses without requiring them, and whether they are enabled |
| `flock ext add <extension>` | Require an extension in composer.json (`composer require ext-<extension>:*`) |
| `flock frankenphp` | Show the current site's server and the installed FrankenPHP builds |
| `flock frankenphp on [site] [--octane\|--classic]` | Serve a site with FrankenPHP, optionally in Laravel Octane's worker mode |
| `flock frankenphp off [site]` | Serve a site with PHP-FPM again (php-cgi on Windows) |
| `flock frankenphp restart [site]` | Restart a site's FrankenPHP, e.g. to load changed code into Octane's workers |
| `flock frankenphp list` | List FrankenPHP builds, installed and available |
| `flock frankenphp install <php-version>` | Install or update FrankenPHP for a PHP version (`--from <dir\|archive>` for a local package) |
| `flock frankenphp uninstall <php-version>` | Remove FrankenPHP for a PHP version |

## Services

| Command | Description |
| --- | --- |
| `flock services [--json]` | List services and their state; `--json` for scripts: each with its port, whether it runs, and the `.env` settings to connect to it |
| `flock services install <service> [version] [--port N]` | Install PostgreSQL, MySQL, MariaDB, MongoDB, Valkey, Redis (macOS), Meilisearch, Typesense (macOS and Linux) or RustFS. `--port` doesn't apply to PostgreSQL |
| `flock services uninstall <service> [version] [--delete-data]` | Remove a version of a service (the active one unless given) |
| `flock services use <service> <version>` | Switch a service to another installed version |
| `flock services update <service> [version]` | Update a version of a service to its newest release (the active one unless given) |
| `flock services list <service>` | List the versions of a service with what is installed, updates and data sizes |
| `flock services create-db <service> <name>...` | Create one or more databases in PostgreSQL, MySQL or MariaDB (the service has to be running) |

## Tools

| Command | Description |
| --- | --- |
| `flock mail` | List caught emails |
| `flock mail open <id>` | Open an email in your system's mail app |
| `flock mail clear [inbox]` | Delete caught emails |
| `flock dumps [on\|off\|clear]` | Capture `dump()` / `dd()` output (and queries, logs, views, jobs and requests), or list captured dumps |
| `flock dumps page [on\|off]` | Also show `dump()` output in the page or terminal while capturing |
| `flock events [kind...] [-n 50]` | List captured queries, logs, views, jobs and requests |
| `flock events [on\|off] <kind...>` | Choose which kinds are captured: `query`, `log`, `view`, `job`, `request` |
| `flock events clear` | Delete captured events |
| `flock xdebug [on\|off]` | Send the requests that ask for Xdebug to a PHP pool that loads it |
| `flock xdebug breakpoints [on\|off]` | Also debug every request of a site with PhpStorm breakpoints |
| `flock debug <php args...>` | Run PHP with Xdebug, e.g. `flock debug artisan migrate` |

## AI agents

| Command | Description |
| --- | --- |
| `flock mcp install [--client <client>]` | Register Flock's MCP server with your AI clients: `claude-code`, `cursor`, `vscode`, `windsurf`, `codex` |
| `flock mcp uninstall [--client <client>]` | Remove Flock from your AI clients |
| `flock mcp status` | Show which AI clients Flock is registered with |
| `flock mcp [--site <site>]` | Run the MCP server (your AI clients start it themselves) |
| `flock boost` | Add Flock's guideline to the current project's Laravel Boost guidelines |

## Shims

These commands are put on your PATH and run the version that the current project uses:

```text
php  composer  node  npm  npx  redis-cli  valkey-cli
psql  pg_dump  pg_restore  pg_dumpall  createdb  dropdb
```
