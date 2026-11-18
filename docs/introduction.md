# Introduction

Flock is a free, open-source local development environment for Laravel on **Windows**, **macOS** and **Linux**. It installs and manages everything a Laravel app needs, and it runs natively on your machine, without Docker or a virtual machine.

## What Flock manages

- **Sites**: every project is served on a `.test` domain, optionally over HTTPS with a locally trusted certificate.
- **PHP**: versions 8.1 to 8.5 side by side (on Windows also 7.4 and 8.0), with the common extensions built in and Xdebug ready to switch on.
- **Node.js**: official builds from Node 16 up, chosen per project from `.nvmrc` or `.flockrc`.
- **Services**: PostgreSQL, MySQL, MariaDB, MongoDB, Valkey, Redis, Meilisearch, Typesense and RustFS.
- **Tools**: a mail catcher, a `dump()` viewer, a Laravel log viewer and one-click Xdebug.

## How it works

Flock is a single `flock` binary that runs as a small background service (the *daemon*):

- An embedded [Caddy](https://caddyserver.com) web server listens on ports 80 and 443 and passes PHP requests to PHP-FPM (macOS and Linux) or a pool of `php-cgi` workers (Windows). A site can also run on [FrankenPHP](php.md#frankenphp), with Laravel Octane's worker mode if you like. For a Laravel app, Flock can run its [Reverb](php.md#laravel-reverb) server, reached at the site's own address.
- An embedded DNS server answers for `*.test`, so new sites work immediately without editing your hosts file.
- A local certificate authority issues HTTPS certificates. Its root is restricted to your local TLD, so it can't be used to impersonate real websites.
- Small **shims** for `php`, `composer`, `node`, `npm`, `npx` and the database clients sit on your PATH and run the version that the current project uses.

On top of that, a native app (a menu bar app on macOS, a tray app on Windows, a window with a top bar icon on Linux) gives you a UI for sites, versions, services, mail, dumps and logs. Almost every action is also available as a [CLI command](cli.md).

## App or CLI

You can drive Flock from the app, from the `flock` command, or mix the two: both talk to the same daemon, so a site you link in the terminal shows up in the app right away.

These docs show one way at a time. Use the **App | CLI** switch at the top of the sidebar, or the tabs above each set of steps, to choose. Your choice is remembered.

:::app
You're reading the **App** version: steps say where to click in the menu bar or tray panel and in the Settings window. On Linux, the Flock panel is the menu of Flock's icon in the top bar, and Settings is the Flock window. Its main menu also opens Mail, Dumps and Logs.
:::

:::cli
You're reading the **CLI** version: steps show the `flock` commands to run in a terminal.
:::

## Not just Laravel

Flock serves `public/index.php` when a project has one, which covers Laravel, Symfony, Statamic, Craft and most other PHP frameworks. Otherwise it serves the project folder itself, which also covers plain PHP and static sites.

> Other languages and frameworks are on the roadmap.

## Next steps

- [Install Flock](installation.md)
- [Get your first site running](quick-start.md)
