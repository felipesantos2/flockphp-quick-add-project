package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"flock/internal/archive"
	"flock/internal/download"
)

const wordpressURL = "https://wordpress.org/wordpress-7.1.4.tar.gz"
const wordpressDownloadTimeout = 120 * time.Second

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
