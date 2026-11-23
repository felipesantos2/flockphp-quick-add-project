package worktree

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLoadMergesTheFilesInOrder(t *testing.T) {
	site, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(wt, ConfigFile), `worktrees:
  database: copy
  copy: [storage/app]
  env:
    REDIS_PREFIX: "{{branch}}_"
    DEBUG: true
  hooks:
    after_create:
      - php artisan storage:link
`)
	write(t, filepath.Join(site, LocalFile), `worktrees:
  auto: false
  copy: [.env.testing]
  env:
    DEBUG: false
`)
	write(t, filepath.Join(wt, LocalFile), `worktrees:
  hooks:
    after_create: [npm run build]
`)

	s, err := Load(site, wt)
	if err != nil {
		t.Fatal(err)
	}
	if s.Auto || s.Database != "copy" || !s.Migrate || s.Path != DefaultPath {
		t.Errorf("single values: %+v", s)
	}
	if !slices.Equal(s.Copy, []string{"storage/app", ".env.testing"}) {
		t.Errorf("copy lists add up: %v", s.Copy)
	}
	if !slices.Equal(s.AfterCreate, []string{"php artisan storage:link", "npm run build"}) {
		t.Errorf("hooks add up: %v", s.AfterCreate)
	}
	if !slices.Equal(s.EnvKeys, []string{"REDIS_PREFIX", "DEBUG"}) || s.Env["DEBUG"] != "false" || s.Env["REDIS_PREFIX"] != "{{branch}}_" {
		t.Errorf("env merges key by key, as written: %v %v", s.EnvKeys, s.Env)
	}

	write(t, filepath.Join(wt, ConfigFile), "worktrees:\n  database: clone\n")
	if _, err := Load(site, wt); err == nil || !strings.Contains(err.Error(), "shared, copy or fresh") {
		t.Errorf("an unknown database mode: %v", err)
	}
}

func TestDotenvChangesOnlyTheKeysItSets(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	write(t, path, "# app\nAPP_NAME=\"My App\"\nAPP_URL=http://acme.test # the site\n\nexport MAIL_FROM=old@acme.test\nAPP_URL=http://late.test\n")
	env, err := ReadDotenv(path)
	if err != nil {
		t.Fatal(err)
	}
	if env.Get("APP_NAME") != "My App" || env.Get("APP_URL") != "http://late.test" || env.Has("DB_HOST") {
		t.Fatalf("Get: %q %q", env.Get("APP_NAME"), env.Get("APP_URL"))
	}
	env.Set("APP_URL", "https://feature.acme.test")
	env.Set("APP_NAME", "Feature")
	env.Set("MAIL_FROM", "hi@feature.acme.test")
	env.Set("REDIS_PREFIX", "feature_")
	env.Set("GREETING", "hello world")
	env.Set("WIN_PATH", `C:\Code\acme\database.sqlite`)
	env.Set("WIN_SPACES", `C:\My Code\acme`)
	if err := env.Write(path); err != nil {
		t.Fatal(err)
	}
	want := "# app\nAPP_NAME=\"Feature\"\nAPP_URL=http://acme.test # the site\n\nexport MAIL_FROM=hi@feature.acme.test\nAPP_URL=https://feature.acme.test\nREDIS_PREFIX=feature_\nGREETING=\"hello world\"\nWIN_PATH=C:\\Code\\acme\\database.sqlite\nWIN_SPACES='C:\\My Code\\acme'\n"
	if got := read(t, path); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRunSetsAWorktreeUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hooks here are sh")
	}
	site, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(site, ".env"), "APP_URL=http://acme.test\nSESSION_DOMAIN=acme.test\nSANCTUM_STATEFUL_DOMAINS=localhost,acme.test:5173\nDB_CONNECTION=sqlite\n")
	write(t, filepath.Join(site, "vendor", "autoload.php"), "<?php\n")
	write(t, filepath.Join(site, "storage", "app", "avatar.png"), "png")
	write(t, filepath.Join(wt, "storage", "app", ".gitignore"), "*\n")
	write(t, filepath.Join(site, "secrets.json"), "{}")
	write(t, filepath.Join(wt, ConfigFile), `worktrees:
  copy: [secrets.json, ../outside]
  env:
    REDIS_PREFIX: "{{branch}}_"
  hooks:
    after_create:
      - echo "$FLOCK_WORKTREE_URL {{parent}}" > hook.txt
`)
	settings, err := Load(site, wt)
	if err != nil {
		t.Fatal(err)
	}
	job := Job{Site: site, Path: wt, Parent: "acme", Branch: "feature/auth", Domain: "feature-auth.acme.test",
		ParentDomain: "acme.test", Secured: true, Settings: settings, Flock: "/nonexistent/flock"}

	var log bytes.Buffer
	var steps []string
	if err := job.Run(context.Background(), &log, func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}

	env := read(t, filepath.Join(wt, ".env"))
	for _, want := range []string{
		"APP_URL=https://feature-auth.acme.test\n", "SESSION_DOMAIN=feature-auth.acme.test\n",
		"SANCTUM_STATEFUL_DOMAINS=localhost,feature-auth.acme.test:5173\n",
		"DB_DATABASE=" + filepath.Join(site, "database", "database.sqlite") + "\n", "REDIS_PREFIX=feature-auth_\n",
	} {
		if !strings.Contains(env, want) {
			t.Errorf(".env lacks %q:\n%s", want, env)
		}
	}
	if read(t, filepath.Join(wt, "secrets.json")) != "{}" {
		t.Error("secrets.json wasn't copied")
	}
	if _, err := os.Stat(filepath.Join(wt, "storage", "app", "avatar.png")); err == nil {
		t.Error("a path the worktree has is never copied over")
	}
	if got := read(t, filepath.Join(wt, "hook.txt")); got != "https://feature-auth.acme.test acme\n" {
		t.Errorf("hook: %q", got)
	}
	if !slices.Contains(steps, StepHooks) {
		t.Errorf("steps: %v", steps)
	}
	if !strings.Contains(log.String(), "../outside is outside the project") {
		t.Errorf("log: %s", log.String())
	}

	// Again: nothing is copied over what's there, .env keeps its edits apart from the rules.
	write(t, filepath.Join(wt, ".env"), env+"MINE=1\n")
	if err := job.Run(context.Background(), &log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(wt, ".env")), "MINE=1\n") {
		t.Error("running again replaced the worktree's .env")
	}
}

func TestAFailingHookFailsTheSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hooks here are sh")
	}
	wt := t.TempDir()
	job := Job{Site: t.TempDir(), Path: wt, Domain: "x.acme.test", ParentDomain: "acme.test",
		Settings: Settings{AfterCreate: []string{"false", "touch never"}}}
	err := job.Run(context.Background(), &bytes.Buffer{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "`false` failed") {
		t.Fatalf("want the failing command named, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "never")); err == nil {
		t.Error("the hook went on after a failure")
	}
}

func TestManagerRunsOneSetupPerWorktreeAndJoinsIt(t *testing.T) {
	m := NewManager(t.TempDir())
	release := make(chan struct{})
	runs := 0
	setup := func(ctx context.Context, log io.Writer, step func(string)) error {
		runs++
		step(StepCopy)
		<-release
		return nil
	}
	done := make(chan error, 1)
	first := m.Start("/code/acme-x", "x.acme", setup, func(err error) { done <- err })
	second := m.Start("/code/acme-x/", "x.acme", setup, nil)
	if first.State != Queued || second.Log != "worktree-x.acme" {
		t.Fatalf("first %+v, second %+v", first, second)
	}
	deadline := time.Now().Add(2 * time.Second)
	for m.Get("/code/acme-x").Step != StepCopy {
		if time.Now().After(deadline) {
			t.Fatalf("never running: %+v", m.Get("/code/acme-x"))
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	if err := <-done; err != nil || runs != 1 {
		t.Fatalf("err %v, runs %d", err, runs)
	}
	if st := m.Get("/code/acme-x"); st.State != Ready || st.Step != "" {
		t.Errorf("finished: %+v", st)
	}
	m.Forget("/code/acme-x")
	if m.Get("/code/acme-x") != nil {
		t.Error("Forget kept the status")
	}
}

func TestCreateMakesTheWorktreeWhereFlockYmlSays(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	site := filepath.Join(t.TempDir(), "acme")
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", site, "-c", "user.name=Flock", "-c", "user.email=flock@example.test"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.Mkdir(site, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")
	git("branch", "existing")
	ctx := context.Background()

	path, branch, err := Create(ctx, site, "acme", "feature/auth", "", "")
	if err != nil || path != filepath.Join(filepath.Dir(site), "acme-feature-auth") || branch != "feature/auth" {
		t.Fatalf("default path: %q %q %v", path, branch, err)
	}
	if _, _, err = Create(ctx, site, "acme", "existing", "", filepath.Join(t.TempDir(), "x")); err != nil {
		t.Fatalf("an existing branch: %v", err)
	}
	// No branch: one with a name of its own, from the branch asked for.
	path, branch, err = Create(ctx, site, "acme", "", "existing", "")
	if err != nil || !regexp.MustCompile(`^[a-z]+-[a-z]+$`).MatchString(branch) || path != filepath.Join(filepath.Dir(site), "acme-"+branch) {
		t.Fatalf("a new name: %q %q %v", path, branch, err)
	}

	branches, err := ListBranches(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(branches.Local, "existing") || !slices.Contains(branches.Local, branch) || branches.Remote == nil {
		t.Errorf("branches: %+v", branches)
	}
	if branches.Current == "" || branches.Default != branches.Current {
		t.Errorf("a repository without origin defaults to its main branch, which is checked out: %+v", branches)
	}

	for _, c := range []struct{ branch, from, path string }{
		{"feature/auth", "", ""},                 // checked out already
		{"existing", "main", t.TempDir() + "/y"}, // can't start from elsewhere
		{"bad..name", "", ""},                    // not a branch name
		{"other", "", filepath.Dir(site)},        // the folder exists
	} {
		if _, _, err := Create(ctx, site, "acme", c.branch, c.from, c.path); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: want ErrInvalid, got %v", c, err)
		}
	}
}

func TestALaravelWorktreeGetsKeysOfItsOwn(t *testing.T) {
	site, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(wt, "artisan"), "")
	write(t, filepath.Join(wt, "composer.json"), `{"require":{"laravel/horizon":"^5.0"}}`)
	write(t, filepath.Join(site, ".env"), "APP_NAME=\"Watch Tower\"\nCACHE_PREFIX=wt_cache_\nSESSION_DOMAIN=.watchtower.test\n")
	write(t, filepath.Join(wt, ".env"), "APP_NAME=\"Watch Tower\"\nCACHE_PREFIX=wt_cache_\nSESSION_DOMAIN=.watchtower.test\nREDIS_PREFIX=mine_\n")
	job := Job{Site: site, Path: wt, Domain: "feature-x.watchtower.test", ParentDomain: "watchtower.test", Settings: Defaults()}

	if err := job.writeEnv(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	env := read(t, filepath.Join(wt, ".env"))
	for _, want := range []string{
		"REDIS_PREFIX=mine_\n",                          // the worktree's own: left alone
		"CACHE_PREFIX=wt_cache_feature_x_\n",            // the site's, with the worktree's slug
		"HORIZON_PREFIX=watch_tower_feature_x_horizon:", // Laravel's default shape
		"SESSION_COOKIE=watch_tower_feature_x_session",  // the cookie is shared with the site
	} {
		if !strings.Contains(env, want) {
			t.Errorf(".env lacks %q:\n%s", want, env)
		}
	}

	// Set up again, nothing changes.
	if err := job.writeEnv(&bytes.Buffer{}); err != nil || read(t, filepath.Join(wt, ".env")) != env {
		t.Errorf("again: %v\n%s", err, read(t, filepath.Join(wt, ".env")))
	}

	// Not Laravel: no prefixes.
	plain := t.TempDir()
	write(t, filepath.Join(plain, ".env"), "APP_NAME=x\n")
	job.Path = plain
	if err := job.writeEnv(&bytes.Buffer{}); err != nil || strings.Contains(read(t, filepath.Join(plain, ".env")), "PREFIX") {
		t.Errorf("not Laravel: %v\n%s", err, read(t, filepath.Join(plain, ".env")))
	}
}

func TestLaravelStepsRunWhereTheProjectNeedsThem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake flock is sh")
	}
	site, wt := t.TempDir(), t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	flock := filepath.Join(t.TempDir(), "flock")
	write(t, flock, "#!/bin/sh\necho \"$*\" >> "+calls+"\n")
	if err := os.Chmod(flock, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt, "artisan"), "")
	write(t, filepath.Join(wt, "vendor", "laravel", "boost", "composer.json"), "{}")
	write(t, filepath.Join(wt, "composer.json"), `{"require-dev":{"laravel/boost":"^2.0"}}`)
	write(t, filepath.Join(wt, "package.json"), `{"scripts":{"build":"vite build"},"devDependencies":{"laravel-vite-plugin":"^2.0"}}`)
	write(t, filepath.Join(wt, "vite.config.js"), "")
	write(t, filepath.Join(site, "storage", "app", "public", ".gitignore"), "")
	if err := os.MkdirAll(filepath.Join(site, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(site, "storage", "app", "public"), filepath.Join(site, "public", "storage")); err != nil {
		t.Fatal(err)
	}
	job := Job{Site: site, Path: wt, Domain: "x.acme.test", ParentDomain: "acme.test", Settings: Defaults(), Flock: flock}

	job.laravelSteps(context.Background(), &bytes.Buffer{}, func(string) {})
	want := "exec php artisan storage:link\nexec php artisan boost:update --no-interaction\nexec npm run build\n"
	if got := read(t, calls); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	// Built and linked already, and build turned off: only Boost.
	write(t, filepath.Join(wt, "public", "build", "manifest.json"), "{}")
	write(t, filepath.Join(wt, "public", "storage"), "")
	job.Settings.Build = false
	_ = os.Remove(calls)
	job.laravelSteps(context.Background(), &bytes.Buffer{}, func(string) {})
	if got := read(t, calls); got != "exec php artisan boost:update --no-interaction\n" {
		t.Errorf("again: %q", got)
	}
}
