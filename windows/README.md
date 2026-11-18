# Flock for Windows

The Windows version is the same Go daemon and CLI as on macOS (`cmd/flock`), plus a native
WinUI 3 tray app (`windows/Flock.Tray`) that mirrors the macOS menu bar app: a panel from the
notification area icon, and the Mail, Dumps, Logs and Settings windows.

```
windows/
  Flock.Kit/         models + API client (net10.0, no UI — the macOS app's FlockKit)
  Flock.Kit.Tests/   xUnit tests, run anywhere: dotnet test windows/Flock.Kit.Tests
  Flock.Tray.Tests/  tray state tests, run on Windows: dotnet test windows/Flock.Tray.Tests -p:Platform=x64
  Flock.Tray/        the WinUI 3 app (unpackaged, self-contained Windows App SDK)
  Flock.Screenshots/ the app's windows with demo data, as PNGs for the docs (never shipped)
  installer/        the Inno Setup script for Flock-Setup-x64.exe
  build.ps1         tests + publishes FlockTray to windows\dist\FlockTray-x64\
  build-installer.ps1  the CLI, the hosts helper, build.ps1 and the installer around them
  sign.ps1          Artifact Signing for the above, when CI sets it up (local builds stay unsigned)
```

The UI uses compiled WinUI XAML with adjacent C# code-behinds for lifecycle, native-window, and
runtime-only behavior. It uses the same WinUI controls, and the project compiles (with full type
checking) on macOS and Linux too:

```sh
dotnet msbuild windows/Flock.Tray -restore -t:Compile -p:RuntimeIdentifier=win-x64 -p:Platform=x64 -p:WindowsAppSDKSelfContained=false
```

Running and publishing it needs Windows (the Windows App SDK's packaging tools are Windows programs).

## How Flock works on Windows

| macOS | Windows |
| --- | --- |
| launchd agent keeps `flock daemon` running | Scheduled task "Flock Daemon" starts `flockd.exe daemon --watchdog` at sign-in; the watchdog restarts a crashed daemon. `flockd.exe` is `flock.exe` marked as a GUI program, so no console window appears. |
| `/etc/resolver/test` → Flock's DNS server | An **NRPT rule** (`Get-DnsClientNrptRule`, comment "Flock") sends every `*.test` name to Flock's DNS server on 127.0.0.1:53 — port 53 because NRPT rules can't name a port. It answers A records only (sites listen on IPv4 only; an AAAA `::1` answer cost PHP ~2 s per connection). So every subdomain works, up to 4 levels below a site or alias (`a.b.c.d.app.test`). The daemon checks every minute that lookups really arrive (Group Policy, VPN clients or a program on port 53 can get in the way) and reports it in `flock status` / Settings. |
| — | Fallback: the **hosts helper**, a small Windows service (LocalSystem), copies site domains and their extra domains (`flock domain add tenant1`, `flock domain add app-2.test`) from `%AppData%\Flock\hosts.txt` into a marked block of the hosts file. It only accepts `*.<tld>` names → 127.0.0.1. These names resolve even where the NRPT rule is overridden, and in browsers whose own secure DNS (DoH in "secure" mode) skips NRPT. It is its own program, `flock-helper.exe` (Caddy, linked into `flock.exe`, claims the service dispatcher of any process that runs as a service); it logs failures to `C:\Program Files\Flock\hosts-helper.log`. |
| PHP-FPM pool per version | 6 `php-cgi.exe` workers per version on 127.0.0.1:19000+ (e.g. 19840–19845 for 8.4); Caddy balances requests to the least busy one. |
| PHP from the binaries manifest: Flock's own build (`bin/php`, `php-fpm`, shared extensions) | PHP from the binaries manifest: the official windows.php.net build (thread-safe x64) plus Flock's extension pack (apcu, igbinary, imagick, mongodb, pcov, redis, xdebug), verified by SHA-256 |
| Shell script shims in `bin/` | `php.exe`, `composer.exe`, `node.exe`, … in `%AppData%\Flock\bin` are hard links to `flock.exe`, which acts as `flock exec <tool>` under those names. The folder is prepended to the user PATH. |
| CA trusted in the System keychain | CA added to the machine's Trusted Root store (`certutil -addstore Root`). PHP gets a bundle of the Windows root store + Flock's CA. |

`flock install` asks for administrator rights once (UAC), for the hosts helper, the NRPT rule and the
CA. It asks again only when the helper's revision changed (`hosts.HelperRevision`, raised with
changes to the helper, not with every release), the rule is missing, or either is set up for
another TLD. It asks before replacing anything: declining leaves the installed version as it was.
Updates do the same: the installer runs the new `flock.exe install --privileged-only` before it
replaces the app, and stops when the prompt is declined.

Everything comes from the binaries manifest of the configured channel (stable, beta), verified by
SHA-256 against a signed list — nothing is built on the machine:

| Service | macOS | Windows |
| --- | --- | --- |
| PostgreSQL (`flock services install postgres`), with `psql`, `pg_dump` and the other client tools | built by Flock | built by Flock |
| Valkey (`flock services install valkey`) | built by Flock | built by Flock with MSYS2 (`fork()` for snapshots) |
| Redis (`flock services install redis`) | built by Flock | not offered — use Valkey, which speaks the same protocol |
| MySQL, MongoDB (`flock services install <id> [line]`) | the official builds | the official zips (Flock drops the debug symbols) |
| MariaDB | built by Flock | the official zip |
| Meilisearch, RustFS | the official binaries | the official binaries |
| Typesense | the official build | not available (no Windows build) |
| Node.js | the official builds | the official builds |

Running `psql` in a terminal before PostgreSQL is installed offers to install it; `redis-cli`
talks to the installed Redis, else to Valkey. PostgreSQL, like PHP, needs the
[Visual C++ Redistributable](https://aka.ms/vs/17/release/vc_redist.x64.exe).

Flock's PHP includes the phpredis extension on Windows too, so Laravel's default
`REDIS_CLIENT=phpredis` works without a Composer package.

Services are stopped gracefully, as on macOS, although Windows has no signals: PostgreSQL through
`pg_ctl stop -m fast`, Valkey with a `SHUTDOWN` command (which saves its snapshot). Only when that
fails, or a service hasn't exited after 10 seconds, it is killed. php-cgi workers are just killed.

## Trying it on a Windows machine

1. **Build the CLI** (on any machine with Go): `make windows`, which writes `flock.exe` and
   `flock-helper.exe` to `dist/windows-amd64/`. Copy the folder to the Windows machine (both files,
   side by side). Only x64 is built for now; Windows on ARM runs it under emulation.
2. **Install**: in PowerShell, `.\flock.exe install`. Expect a UAC prompt. Then open a **new**
   terminal so the PATH change applies.
3. **PHP**: `flock php install 8.5`, or the tray app (step 4) → Settings → PHP → Install. In a
   terminal, just running `php` offers to install a missing version (Node.js too). A zip from
   windows.php.net also works: `flock php install --from php-8.5.x-nts-Win32-vs17-x64.zip`.
   PHP needs the [Visual C++ Redistributable](https://aka.ms/vs/17/release/vc_redist.x64.exe).
4. **Tray app**: install the .NET 10 SDK (`winget install Microsoft.DotNet.SDK.10`), then
   `.\windows\build.ps1` and start `windows\dist\FlockTray-x64\FlockTray.exe`.
   `FlockTray.exe --selftest` checks every API call against the running daemon.

### Running the development tray alongside an installed copy

Run `dotnet run --project windows/Flock.Tray -p:Platform=x64` from the repository root, or launch the executable produced by `windows/build.ps1`.
An executable without the installer's adjacent `unins000.exe` runs as **Flock Dev**, in Debug or Release. Use `--dev` to force this mode when testing
from an installed directory.

Each local executable path has its own single-instance mutex and activation window. Starting that executable again opens its existing dev window;
it never hands the launch to the installed production tray. Windows show `[Dev]`, and the tray tooltip and panel header show **Flock Dev**.

Dev builds use separate tray preferences and WebView2 data under `%LocalAppData%\Flock\Flock.Tray.Dev.<path-hash>`. Versioned dev builds support the
same update checks, signature verification, and installer launch as production. The downloaded installer updates the installed app, not the local build output.
Automatic bundled CLI replacement is disabled. The dev tray still connects to the installed daemon by default: its sites, services, and daemon version are real,
and actions in the dev UI affect that daemon. `FLOCK_HOME` still selects a different daemon data folder when needed.

Rider's default build uses version `0.1.0`, so updates can be tested directly. To test from a specific version, build with
`dotnet build windows/Flock.Tray -p:Platform=x64 -p:InformationalVersion=1.0.0`. For `windows/build.ps1`, set `FLOCK_VERSION=1.0.0` first;
its default Git description may not be a version the updater can compare.

### First-run checklist

- [ ] `flock install` finishes; Task Scheduler shows "Flock Daemon"; `flock status` answers
- [ ] No console windows appear (daemon, php-cgi, postgres all run hidden)
- [ ] Services shows "Hosts file" running; the hosts file has a `# BEGIN Flock` block after linking a site
- [ ] `flock status` says `*.test → 127.0.0.1, any subdomain`; `https://a.b.<name>.test` serves the site without certificate warnings
- [ ] `flock domain add tenant1` → `tenant1.<name>.test` appears in the hosts file within a few seconds
- [ ] `flock link` in a Laravel project → `http://<name>.test` serves it; `flock secure` → HTTPS without warnings in Edge/Chrome
- [ ] `php -v`, `composer -V`, `php artisan` in a new terminal use Flock's PHP (`where.exe php` lists `%AppData%\Flock\bin\php.exe` first)
- [ ] `composer install` reaches packagist (CA bundle with the Windows roots)
- [ ] Settings → PHP: install 8.4, switch a site to it, uninstall it again (files must not be locked)
- [ ] A PhpStorm breakpoint hits on reload with no browser extension, and the site goes back to the fast pool once it is removed
- [ ] `?XDEBUG_TRIGGER=1` and the Xdebug browser extension hit a breakpoint; requests without them never load Xdebug
- [ ] `flock debug artisan …` hits a breakpoint; plain `php` does not load Xdebug
- [ ] `flock services install valkey` downloads Valkey and starts it; `redis-cli ping` → PONG; `flock services install postgres` → `psql -c "select 1"`
- [ ] Upgrading from a Flock that had Redis: it keeps running from `services\redis\7.2.16` with its data
- [ ] Settings → Services → All: install MySQL, MariaDB (port suggested as 3307 next to MySQL), MongoDB, Valkey, Meilisearch, RustFS; each starts and its connection works; Typesense and Redis show as not available
- [ ] MySQL 8.4 installs next to 9.7 (9.7 keeps running); Switch… runs 8.4 on the same port with its own data; uninstalling the active one hands over to the other; `flock services use mysql 9.7` switches back
- [ ] Stopping Redis/MySQL/MariaDB/PostgreSQL in Services (or `flock restart`) logs a clean shutdown (redis.log: "Redis is now ready to exit"; mysql.log: "Shutdown complete"; postgres.log: "database system is shut down"), not a kill
- [ ] Mail: send from tinker → appears in Mail, HTML view, attachments save
- [ ] Dumps: `dump()` in a route → Dumps window, the file link opens the editor
- [ ] `flock restart` and the tray's Restart bring the daemon back with a new pid
- [ ] Ports 80/443 taken (IIS, Skype…) → the warning names the program
- [ ] Sign out and in → daemon starts; with "Open Flock at sign-in" on, the tray icon too
- [ ] `flock uninstall` removes the task, PATH entry, hosts block, helper service, CA and Flock's
      files (binaries, PHP/Node versions, certificates, logs, caches), keeping only `config.json`
      and the services' data (PostgreSQL, MySQL, Redis, ...); `flock uninstall --purge` removes those too

Logs: `%AppData%\Flock\log\` (daemon.log, web.log, php8.x-cgi.log, php8.x-install.log) and
`%LocalAppData%\Flock\tray.log` for unexpected errors in the tray app.

## Translations

The app's and the installer's texts come from [`lang/`](../lang/README.md), which the three apps share: add or
change a text there, then run `make i18n` (`go run ./tools/i18n`) from the repository root. It writes
`Flock.Kit/Strings.resx` with a `Strings.<language>.resx` per language (a text a language lacks falls back to
English), `Flock.Kit/Languages.g.cs` with the languages and their plural rules, and `installer/Strings.iss`. Code
uses the public `Strings` class the build generates from `Strings.resx` (into `obj\`, no Designer file is
committed): `Strings.Sites_Title` for the key `sites.title`. XAML uses `{l:Loc Key=Sites_Title}`
(`xmlns:l="using:Flock.Tray"`), counts use `Localization.Plural` with the `_one`/`_other`/... forms.
`ResourcesTests` in Flock.Kit.Tests fails for a broken translation, a changed placeholder, an unknown key in XAML
or untranslated text in XAML.

Settings → General → Language picks the language (Windows' preferred languages by default); it applies after
Relaunch Flock, which starts a new instance (`--relaunch=<pid>`, waiting for the old one to exit). The installer speaks
the Windows display language; the app's updater passes it the app's language with `/LANG`.

## Screenshots for the docs

`Flock.Screenshots` renders the tray app's windows with demo data, for the website's docs: every
shot in `Flock.Screenshots/Screenshots.cs`, in the light and the dark theme, as
`{shot}-windows-{light|dark}.png` (the shot ids are the website's `screenshot:…` placeholders).

```powershell
dotnet build windows/Flock.Screenshots -c Release -p:Platform=x64
windows\Flock.Screenshots\bin\x64\Release\net10.0-windows10.0.22621.0\win-x64\Flock.Screenshots.exe --out screenshots
# or a few: --only settings-php,mail-window   one theme: --theme dark
```

It needs no daemon and changes nothing on the machine: a fake daemon answers from
`tools/screenshots/fixtures` (Flock's data folder points at a scratch folder through `FLOCK_HOME`).
The windows open on screen, on top of everything, for a few seconds each; the PNGs are what the
screen shows, at the display's scale. `tools/screenshots/make-dumps.php` regenerates the Dumps
fixtures with the dump server's VarDumper. The **Screenshots** workflow does the same in CI.

The project references `Flock.Tray` through `ProjectReference`, so it renders the same assembly that ships. Its own entry point supplies the theme,
isolated preferences, and startup callback, with background polling and update checks disabled. Assets flow through the project reference; fixtures
belong only to the capture runner. `build.ps1` checks that the published tray contains neither the capture runner nor its fixtures.
