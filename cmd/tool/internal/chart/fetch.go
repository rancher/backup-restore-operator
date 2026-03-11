package chart

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// versionRe restricts version tags to the expected semver-with-v format (e.g. v9.0.0, v2.1.0-rc.1).
// This prevents path-traversal attacks when the version is used as a cache directory segment.
var versionRe = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[A-Za-z0-9.]+)?$`)

var httpClient = &http.Client{Timeout: 60 * time.Second}

// githubReleaseFmt is the URL for a packaged rancher-backup helm chart on a GitHub release.
// The version path segment keeps the "v" prefix; the filename strips it (matching HELM_CHART_VERSION).
const githubReleaseFmt = "https://github.com/rancher/backup-restore-operator/releases/download/%s/rancher-backup-%s.tgz"

// CacheDir returns the bro-tool cache directory.
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving user cache dir: %w", err)
	}
	return filepath.Join(base, "bro-tool"), nil
}

// FetchChartByVersion returns the path to the packaged rancher-backup chart for the given BRO
// version tag (e.g. "v2.1.0"). Downloads and caches the .tgz from GitHub if not already present.
func FetchChartByVersion(version string) (string, error) {
	if !versionRe.MatchString(version) {
		return "", fmt.Errorf("invalid version %q: must match %s", version, versionRe)
	}

	cacheBase, err := CacheDir()
	if err != nil {
		return "", err
	}

	// Chart filename uses the version without the leading "v" (matching HELM_CHART_VERSION).
	chartVersion := strings.TrimPrefix(version, "v")
	filename := fmt.Sprintf("rancher-backup-%s.tgz", chartVersion)
	cachePath := filepath.Join(cacheBase, "charts", version, filename)

	if _, err := os.Stat(cachePath); err == nil {
		logrus.Debugf("Using cached chart for version %s at %s", version, cachePath)
		return cachePath, nil
	}

	url := fmt.Sprintf(githubReleaseFmt, version, chartVersion)
	logrus.Infof("Fetching chart for version %s from GitHub...", version)

	if err := downloadFile(url, cachePath); err != nil {
		return "", fmt.Errorf("fetching chart for version %s: %w", version, err)
	}

	logrus.Debugf("Cached chart for version %s at %s", version, cachePath)
	return cachePath, nil
}

func downloadFile(url, destPath string) error {
	resp, err := httpClient.Get(url) // #nosec G107 -- URL is constructed from a validated version string
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", destPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(destPath) // clean up partial download
		return fmt.Errorf("writing %s: %w", destPath, err)
	}
	return nil
}
