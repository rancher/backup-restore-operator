package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	ghrelease "github.com/mallardduck/ghreleases"
	"github.com/rancher/backup-restore-operator/automation-core/scripts/internal/actionindex"
	"gopkg.in/yaml.v3"
)

func main() {
	indexPath := flag.String("index", "action-versions.yaml", "Path to action-versions.yaml")
	githubToken := flag.String("token", os.Getenv("GITHUB_TOKEN"), "GitHub token")
	dryRun := flag.Bool("dry-run", false, "Don't write changes, just print what would be updated")
	flag.Parse()

	if *githubToken == "" {
		log.Fatal("GitHub token required (set GITHUB_TOKEN or use -token flag)")
	}

	// Load current index
	currentIndex, err := actionindex.Load(*indexPath)
	if err != nil {
		log.Fatalf("Failed to load action index: %v", err)
	}

	// Create GitHub client
	client := ghrelease.NewClient(*githubToken)

	updated := false
	updates := []string{}

	// Check each action for updates
	for actionName, currentVersion := range currentIndex.Actions {
		log.Printf("Checking %s (current: %s)", actionName, currentVersion.Version)

		// Parse owner/repo
		parts := strings.Split(actionName, "/")
		if len(parts) < 2 {
			log.Printf("  Skipping %s: invalid action name format", actionName)
			continue
		}
		owner := parts[0]
		repo := parts[1]

		// Get latest release
		ctx := context.Background()
		latestTag, err := client.LatestRelease(ctx, owner, repo)
		if err != nil {
			log.Printf("  Error fetching latest release: %v", err)
			continue
		}

		// Resolve tag to commit SHA using ghreleases v0.6.0
		sha, err := client.ResolveTagCommit(ctx, owner, repo, latestTag)
		if err != nil {
			log.Printf("  Error resolving tag to commit SHA: %v", err)
			continue
		}

		// Check if update needed
		if latestTag != currentVersion.Version || sha != currentVersion.SHA {
			log.Printf("  Update available: %s -> %s (SHA: %s -> %s)",
				currentVersion.Version, latestTag,
				currentVersion.SHA[:7], sha[:7])

			currentIndex.Actions[actionName] = actionindex.ActionVersion{
				Version: latestTag,
				SHA:     sha,
			}
			updated = true
			updates = append(updates, fmt.Sprintf("%s: %s -> %s", actionName, currentVersion.Version, latestTag))
		} else {
			log.Printf("  Up to date: %s", currentVersion.Version)
		}
	}

	if !updated {
		log.Println("\nNo updates needed")
		return
	}

	log.Printf("\n%d action(s) updated:", len(updates))
	for _, update := range updates {
		log.Printf("  - %s", update)
	}

	if *dryRun {
		log.Println("\nDry run - no changes written")
		return
	}

	// Write updated index
	if err := writeActionIndex(*indexPath, currentIndex); err != nil {
		log.Fatalf("Failed to write action index: %v", err)
	}

	log.Printf("\nUpdated %s successfully", *indexPath)

	// Write summary for GitHub Actions
	summaryFile := os.Getenv("GITHUB_STEP_SUMMARY")
	if summaryFile != "" {
		summary := "## Action Index Updates\n\n"
		for _, update := range updates {
			summary += fmt.Sprintf("- %s\n", update)
		}
		os.WriteFile(summaryFile, []byte(summary), 0644)
	}

	// Set output for GitHub Actions
	githubOutput := os.Getenv("GITHUB_OUTPUT")
	if githubOutput != "" {
		output := fmt.Sprintf("updated=true\ncount=%d\n", len(updates))
		f, err := os.OpenFile(githubOutput, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			f.WriteString(output)
			f.Close()
		}
	}
}

// writeActionIndex writes the action index back to YAML
func writeActionIndex(path string, index *actionindex.ActionIndex) error {
	// Add header comment
	header := `# GitHub Actions Version Index
# This file is automatically updated by the update-action-index workflow
# Last updated: ` + time.Now().Format("2006-01-02") + `

`

	data, err := yaml.Marshal(index)
	if err != nil {
		return fmt.Errorf("marshal action index: %w", err)
	}

	output := header + string(data)

	if err := os.WriteFile(path, []byte(output), 0644); err != nil {
		return fmt.Errorf("write action index: %w", err)
	}

	return nil
}
