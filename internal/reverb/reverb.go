// Package reverb runs a site's Laravel Reverb server behind Flock's web server, which sends it the
// site's WebSocket connections and the HTTP API Laravel broadcasts through, so browsers reach it at
// wss://<site>.test/app like the rest of the site.
package reverb

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"flock/internal/config"
	"flock/internal/php"
	"flock/internal/supervisor"
)

// Site is a site whose Reverb server runs.
type Site struct {
	Name  string
	Path  string // the project
	Minor string // the PHP version
	Port  int    // loopback port the web server proxies to
	Env   []string
	// Restart is bumped to restart the process, e.g. so it loads changed code.
	Restart int
}

// Installed reports whether a project is a Laravel app with Laravel Reverb installed.
func Installed(path string) bool {
	return exists(filepath.Join(path, "artisan")) && exists(filepath.Join(path, "vendor", "laravel", "reverb", "composer.json"))
}

// Spec is the site's `php artisan reverb:start`. The PHP version must be installed.
func Spec(root string, s Site) supervisor.Spec {
	name := config.ReverbPortID(s.Name)
	// php.Env rather than CLIEnv: a server has no use for pcov. FLOCK_RESTART and FLOCK_PHP_VERSION
	// change the spec when the counter changes or a patch update is installed, so the supervisor
	// restarts the process.
	env := append(php.Env(root, s.Minor), s.Env...)
	env = append(env, "FLOCK_RESTART="+strconv.Itoa(s.Restart), "FLOCK_PHP_VERSION="+php.FullVersion(root, s.Minor))
	return supervisor.Spec{
		Name: name,
		Path: php.Binary(root, s.Minor),
		// --hostname: given the site's name (REVERB_HOST), Reverb serves TLS itself when another
		// local tool has a certificate for it, but the web server speaks plain HTTP to it.
		Args:       []string{"artisan", "reverb:start", "--host=127.0.0.1", "--port=" + strconv.Itoa(s.Port), "--hostname=127.0.0.1", "--no-interaction"},
		Env:        env,
		Dir:        s.Path,
		LogFile:    filepath.Join(root, "log", name+".log"),
		StopSignal: syscall.SIGTERM,
	}
}

// Env is what the app's .env needs for its broadcasts and its Echo client to go through the web
// server: the site's host, and the scheme and port it is served on. install:broadcasting's
// VITE_REVERB_* variables refer to these.
func Env(host, scheme string, port int) []string {
	return []string{"REVERB_HOST=" + host, "REVERB_PORT=" + strconv.Itoa(port), "REVERB_SCHEME=" + scheme}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
