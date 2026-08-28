package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Config represents the branch configuration structure
type Config struct {
	Branch            string            `yaml:"branch"`
	K3SVersions       []string          `yaml:"k3s_versions"`
	AutomationCoreRef string            `yaml:"automation_core_ref"`
	Workflows         map[string]bool   `yaml:"workflows"`
	Description       string            `yaml:"description"`
}

// templateFuncs returns custom template functions
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"jsonArray": func(items []string) string {
			// Convert []string to JSON array format: ["item1", "item2"]
			quoted := make([]string, len(items))
			for i, item := range items {
				quoted[i] = fmt.Sprintf(`"%s"`, item)
			}
			return fmt.Sprintf("[%s]", strings.Join(quoted, ", "))
		},
	}
}

// loadConfig reads and parses a YAML config file
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

// renderTemplate processes a template file with the given config
func renderTemplate(templatePath string, cfg *Config) (string, error) {
	// Use custom delimiters to avoid conflicts with GitHub Actions ${{ }}
	tmpl, err := template.New(filepath.Base(templatePath)).
		Delims("[[", "]]").
		Funcs(templateFuncs()).
		ParseFiles(templatePath)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	rendered := buf.String()

	// Validate YAML syntax of rendered output
	var validation interface{}
	if err := yaml.Unmarshal([]byte(rendered), &validation); err != nil {
		return "", fmt.Errorf("invalid YAML output: %w", err)
	}

	return rendered, nil
}

func main() {
	configFile := flag.String("config", "", "Path to config YAML file")
	templateDir := flag.String("template-dir", "", "Directory containing .tmpl files")
	outputDir := flag.String("output-dir", "", "Output directory for rendered files")
	flag.Parse()

	if *configFile == "" || *templateDir == "" || *outputDir == "" {
		log.Fatal("All flags required: -config, -template-dir, -output-dir")
	}

	// Load configuration
	cfg, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Load config: %v", err)
	}

	// Create output directory
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Create output dir: %v", err)
	}

	// Find all .tmpl files
	pattern := filepath.Join(*templateDir, "*.tmpl")
	templates, err := filepath.Glob(pattern)
	if err != nil {
		log.Fatalf("Find templates: %v", err)
	}

	if len(templates) == 0 {
		log.Fatalf("No .tmpl files found in: %s", *templateDir)
	}

	// Render each template
	for _, tmplPath := range templates {
		rendered, err := renderTemplate(tmplPath, cfg)
		if err != nil {
			log.Fatalf("Render %s: %v", filepath.Base(tmplPath), err)
		}

		// Write to output directory (remove .tmpl extension)
		outName := strings.TrimSuffix(filepath.Base(tmplPath), ".tmpl")
		outPath := filepath.Join(*outputDir, outName)

		if err := os.WriteFile(outPath, []byte(rendered), 0644); err != nil {
			log.Fatalf("Write %s: %v", outName, err)
		}

		fmt.Printf("✓ Rendered %s\n", outName)
	}

	fmt.Printf("\nSuccessfully rendered %d workflow(s)\n", len(templates))
}
