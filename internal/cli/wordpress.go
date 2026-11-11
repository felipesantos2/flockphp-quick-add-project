package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"flock/internal/api"
	"flock/internal/archive"
	"flock/internal/catalog"
	"flock/internal/download"
)

const wordpressURL = "https://wordpress.org/latest.tar.gz"
const wordpressDownloadTimeout = 120 * time.Second

func createWordPress(ctx context.Context, s *state, dir, database string, service api.Service) error {
	if err := downloadWordPress(ctx, nil, wordpressURL, dir); err != nil {
		return err
	}
	if err := configureWordPress(dir, database, service.Port); err != nil {
		return fmt.Errorf("configure WordPress: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := catalog.CreateDatabase(ctx, s.root, service.ID, service.Version, service.Port, database); err != nil {
		return fmt.Errorf("create database %s: %w", database, err)
	}
	return nil
}

func downloadWordPress(ctx context.Context, client *http.Client, url, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, wordpressDownloadTimeout)
	defer cancel()

	f, err := os.CreateTemp("", "flock-wordpress-*.tar.gz")
	if err != nil {
		return err
	}
	file := f.Name()
	defer os.Remove(file)
	if err := f.Close(); err != nil {
		return err
	}
	if err := download.File(ctx, client, url, file, "", nil); err != nil {
		return fmt.Errorf("download WordPress (timeout 120s): %w", err)
	}
	if err := archive.Extract(file, dir); err != nil {
		return err
	}
	for _, name := range []string{"index.php", "wp-settings.php", "wp-config-sample.php"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("WordPress archive is missing %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("WordPress archive has no regular file %s", name)
		}
	}
	return nil
}

func configureWordPress(dir, database string, port int) error {
	data, err := os.ReadFile(filepath.Join(dir, "wp-config-sample.php"))
	if err != nil {
		return err
	}
	content := string(data)
	for _, replacement := range [][2]string{
		{"'database_name_here'", "'" + database + "'"},
		{"'username_here'", "'root'"},
		{"'password_here'", "''"},
		{"'localhost'", fmt.Sprintf("'127.0.0.1:%d'", port)},
	} {
		if strings.Count(content, replacement[0]) != 1 {
			return fmt.Errorf("WordPress config sample must contain exactly one %s", replacement[0])
		}
		content = strings.Replace(content, replacement[0], replacement[1], 1)
	}
	const placeholder = "put your unique phrase here"
	if strings.Count(content, placeholder) != 8 {
		return fmt.Errorf("WordPress config sample must contain eight authentication key and salt placeholders")
	}
	for range 8 {
		var key [32]byte
		if _, err := rand.Read(key[:]); err != nil {
			return err
		}
		content = strings.Replace(content, placeholder, hex.EncodeToString(key[:]), 1)
	}
	return os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte(content), 0o600)
}
