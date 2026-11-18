# PHP

Flock installs and manages PHP for you. Several versions live side by side, and each site uses the version it needs.

## Installing versions

PHP 8.1 and newer are supported, and on Windows also 7.4 and 8.0.

:::app
Open **Settings…** from the Flock panel and go to **PHP**. Click **Install** next to a version, or **Update to …** when a newer patch release is out. **Check now** looks for new releases.

![Settings → PHP](screenshot:settings-php)

To remove a version, open its **⋯** menu and choose **Uninstall…**. Uninstalling also removes that version's `php.ini`.
:::

:::cli
```bash
flock php install 8.5
flock php install 8.4
flock php list
```
:::

Every version is a download: Flock checks the checksum and the signature of the list it comes from before installing it. Nothing is compiled on your machine.

- **macOS:** builds for Apple silicon made by Flock, with PHP-FPM.
- **Linux:** builds for x64 and arm64 made by Flock, with PHP-FPM. They don't use or change the PHP your distribution installs.
- **Windows:** the official thread-safe x64 builds from windows.php.net, plus Flock's pack of extensions the official builds don't include. Each version runs a small pool of `php-cgi` workers, and requests go to the least busy one.

Updating installs the newest patch release, and keeps your `php.ini`. When a build turns out to be broken and is withdrawn, Flock replaces it with a fixed one by itself.

## The default version

The default version is used by every site and folder that doesn't choose its own. To choose per project, see [Per-project versions](project-versions.md).

:::app
In **Settings → PHP**, click **Make Default** next to the version you want.
:::

:::cli
```bash
flock use 8.5
```
:::

## Extensions

Flock's PHP includes the extensions most Laravel apps need, all of them on, with nothing to set up:

```text
apcu  bcmath  bz2  calendar  ctype  curl  dom  exif  fileinfo  filter  ftp  gd
gmp  iconv  igbinary  imagick  intl  ldap  mbstring  memcached  mongodb  mysqli
opcache  openssl  pcntl  pdo_mysql  pdo_pgsql  pdo_sqlite  pgsql  phar  posix
readline  redis  session  shmop  simplexml  soap  sockets  sodium  sqlite3
tokenizer  xml  xmlreader  xmlwriter  xsl  zip  zlib
```

- On macOS and Linux, `sysvmsg`, `sysvsem` and `sysvshm` are included too, and on Windows `gettext`.
- **pcov** is on for the command line only (`php`, `composer`, `php artisan test`), so your tests can measure code coverage while web requests stay fast.
- **Xdebug** loads only for requests that ask for it. See [Xdebug](xdebug.md).
- On Windows, `memcached`, `pcntl` and `posix` aren't available, and `mongodb` needs PHP 8.1 or newer.

### Which extensions a project needs

A project should list the extensions it uses in `composer.json`, as `"ext-intl": "*"`. Then `composer install` refuses to run on a server that doesn't have them, instead of the app failing there later. Packages do this for themselves, so most requirements already come with them.

For each site, Flock shows the extensions it requires, which package requires each one, and whether Flock's PHP includes it. When a site requires one Flock doesn't include, the site shows it as a problem.

For Laravel apps, Flock also looks at `.env` and `config/` for extensions the app uses without requiring them: the database driver (`DB_CONNECTION=pgsql` needs `pdo_pgsql`), Memcached or APCu as the cache or session store, Redis through phpredis, and the Imagick driver of Intervention Image. Each one comes with **Add to composer.json**, which runs `composer require ext-<name>:*` in the project. These hints are Laravel-only for now.

:::app
In **Settings → Sites**, click the extensions button (the puzzle piece) next to a site.
:::

:::cli
Run it in the project's folder:

```bash
flock ext
flock ext add pdo_pgsql
```
:::

AI agents get the same list from the `php_extensions` tool of Flock's [MCP server](ai.md).

## php.ini

Each version has its own `php.ini`. Flock writes sensible development defaults the first time and never overwrites your changes, but uninstalling the version removes it:

| Setting | Default |
| --- | --- |
| `memory_limit` | `512M` |
| `upload_max_filesize` / `post_max_size` | `128M` |
| `display_errors` | `On` |
| `opcache.enable` | `On` |

:::app
To edit it, open the version's **⋯** menu in **Settings → PHP** and choose **Open php.ini**.
:::

:::cli
The file is `php/8.5/php.ini` inside Flock's [data directory](configuration.md#data-directory), and `php --ini` prints its path.
:::

## FrankenPHP

Instead of PHP-FPM (`php-cgi` on Windows), a site can run on [FrankenPHP](https://frankenphp.dev). It gets a FrankenPHP process of its own, and Flock's web server keeps serving its domains and certificates. FrankenPHP needs PHP 8.2 or newer. It's downloaded for a PHP version the first time a site uses it, and uses that version's `php.ini`. Requests that ask for [Xdebug](xdebug.md) still go to the regular PHP pool.

A site on FrankenPHP runs in one of two modes:

- **Classic** boots the app for every request, as PHP-FPM does.
- **Laravel Octane** runs Octane's workers, so the app stays booted between requests. It needs Laravel Octane in the project (`composer require laravel/octane`). Flock creates `public/frankenphp-worker.php` in the project from Octane's stub, as `php artisan octane:frankenphp` does. Restart the site's FrankenPHP to load changed code into the workers.

:::app
In **Settings → Sites**, select the site and choose **FrankenPHP** under **Runs on** in its **Server** section. Then pick **Classic** or **Laravel Octane** under **Mode**. **Restart** restarts the site's FrankenPHP.

**Settings → PHP** lists the FrankenPHP builds for your PHP versions. Installing one ahead makes switching a site over immediate.
:::

:::cli
```bash
flock frankenphp on acme            # classic mode when switching from PHP-FPM
flock frankenphp on acme --octane   # Laravel Octane's worker mode
flock frankenphp on acme --classic  # back from Octane to classic mode
flock frankenphp restart acme       # load changed code into Octane's workers
flock frankenphp off acme           # back to PHP-FPM (php-cgi on Windows)
flock frankenphp list               # FrankenPHP builds, installed and available
flock frankenphp install 8.4        # install or update the build for a PHP version
```

Run them without a site name inside the project folder.
:::

## Laravel Reverb

Flock can run a Laravel app's [Reverb](https://reverb.laravel.com) server for its site. Browsers reach it at the site's own address, `wss://acme.test/app`, through Flock's web server, so there's no port to open and no separate certificate. Flock runs `php artisan reverb:start` in the project with the site's PHP version, on a loopback port of its own. The project needs Laravel Reverb (`php artisan install:broadcasting --reverb`).

Only Reverb's traffic goes to it: WebSocket connections under `/app/` and the signed requests Laravel broadcasts through under `/apps/`. Every other request on those paths stays the app's, so a panel at `/app` keeps working. This needs Reverb's default paths, so leave `REVERB_SERVER_PATH` unset.

The app has to send its broadcasts and its Echo client to the site rather than to Reverb's own port. Flock shows the `.env` lines that do this, but doesn't change the project's `.env` itself:

```env
REVERB_HOST=acme.test
REVERB_PORT=443
REVERB_SCHEME=https
```

On a site served over HTTP only, they are `REVERB_PORT=80` and `REVERB_SCHEME=http`. `install:broadcasting --reverb` writes the `VITE_REVERB_*` variables as references to these, so the Echo client follows them. Rebuild the front end (`npm run build`, or restart `npm run dev`) after changing them.

Restart the Reverb server to load changed code. It restarts on its own, so the site's FrankenPHP keeps running, and restarting FrankenPHP keeps Reverb's connections. Reverb's output goes to the `reverb-acme` [log](logs.md).

:::app
In **Settings → Sites**, select the site and turn on **Run Reverb** in its **Reverb** section. It shows the `.env` lines with a **Copy** button, Reverb's state and output, and **Restart**.
:::

:::cli
```bash
flock reverb on acme        # run the site's Reverb server; prints the .env lines
flock reverb acme           # its state and the .env lines
flock reverb restart acme   # load changed code
flock reverb off acme       # stop it
flock logs reverb-acme      # its output
```

Run them without a site name inside the project folder. `flock sites` marks a site with Reverb on as `+ Reverb`.
:::

## Composer

`composer` is included and runs with the PHP version of the current project, so `composer install` always matches the version the browser uses. If the project's PHP version isn't installed yet, `php` and `composer` offer to install it the first time you run them.
