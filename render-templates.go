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

// GoConfig represents Go version configuration
// Supports two formats:
// 1. Simple: go: 1.25
// 2. Explicit: go: {version: 1.25.0, ci_image: go1.25}
type GoConfig struct {
	// When unmarshaled from a string (e.g., "1.25"), this becomes the version
	Version string `yaml:"version,omitempty"`
	// CI image tag override (e.g., "go1.25"). If empty, derived from Version
	CIImage string `yaml:"ci_image,omitempty"`
}

// UnmarshalYAML handles both string and map formats for go configuration
func (g *GoConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try string format first (e.g., "1.25")
	var str string
	if err := unmarshal(&str); err == nil {
		g.Version = str
		return nil
	}

	// Fall back to struct format
	type goConfigAlias GoConfig
	var gc goConfigAlias
	if err := unmarshal(&gc); err != nil {
		return err
	}
	*g = GoConfig(gc)
	return nil
}

// GetVersion returns the full Go version (e.g., "1.25.0")
// If version doesn't have patch, appends ".0"
func (g *GoConfig) GetVersion() string {
	if g.Version == "" {
		return ""
	}
	// Count dots to determine if patch version is present
	parts := strings.Split(g.Version, ".")
	if len(parts) == 2 {
		return g.Version + ".0"
	}
	return g.Version
}

// GetCIImage returns the CI image tag (e.g., "go1.25")
// If ci_image is explicitly set, uses that; otherwise derives from version
func (g *GoConfig) GetCIImage() string {
	if g.CIImage != "" {
		return g.CIImage
	}
	if g.Version == "" {
		return ""
	}
	// Derive from version: "1.25.0" -> "go1.25" or "1.25" -> "go1.25"
	parts := strings.Split(g.Version, ".")
	if len(parts) >= 2 {
		return fmt.Sprintf("go%s.%s", parts[0], parts[1])
	}
	return fmt.Sprintf("go%s", g.Version)
}

// Config represents the branch configuration structure
type Config struct {
	Branch            string          `yaml:"branch"`
	K3SVersions       []string        `yaml:"k3s_versions"`
	AutomationCoreRef string          `yaml:"automation_core_ref"`
	Go                GoConfig        `yaml:"go"`
	Workflows         map[string]bool `yaml:"workflows"`
	Description       string          `yaml:"description"`
}

// templateFuncs returns custom template functions
func templateFuncs(cfg *Config) template.FuncMap {
	return template.FuncMap{
		"jsonArray": func(items []string) string {
			// Convert []string to JSON array format: ["item1", "item2"]
			quoted := make([]string, len(items))
			for i, item := range items {
				quoted[i] = fmt.Sprintf(`"%s"`, item)
			}
			return fmt.Sprintf("[%s]", strings.Join(quoted, ", "))
		},
		"goVersion": func() string {
			return cfg.Go.GetVersion()
		},
		"goCIImage": func() string {
			return cfg.Go.GetCIImage()
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
		Funcs(templateFuncs(cfg)).
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

// RenderResult contains information about a rendered template
type RenderResult struct {
	TemplateName string
	OutputPath   string
}

// renderAllTemplates renders all templates in templateDir using cfg and writes to outputDir
// Returns the list of successfully rendered templates and any error encountered
func renderAllTemplates(configPath, templateDir, outputDir string) ([]RenderResult, error) {
	// Load configuration
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Find all .tmpl files
	pattern := filepath.Join(templateDir, "*.tmpl")
	templates, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("find templates: %w", err)
	}

	if len(templates) == 0 {
		return nil, fmt.Errorf("no .tmpl files found in: %s", templateDir)
	}

	// Render each template
	var results []RenderResult
	for _, tmplPath := range templates {
		rendered, err := renderTemplate(tmplPath, cfg)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", filepath.Base(tmplPath), err)
		}

		// Write to output directory (remove .tmpl extension)
		outName := strings.TrimSuffix(filepath.Base(tmplPath), ".tmpl")
		outPath := filepath.Join(outputDir, outName)

		if err := os.WriteFile(outPath, []byte(rendered), 0644); err != nil {
			return nil, fmt.Errorf("write %s: %w", outName, err)
		}

		results = append(results, RenderResult{
			TemplateName: outName,
			OutputPath:   outPath,
		})
	}

	return results, nil
}

func main() {
	configFile := flag.String("config", "", "Path to config YAML file")
	templateDir := flag.String("template-dir", "", "Directory containing .tmpl files")
	outputDir := flag.String("output-dir", "", "Output directory for rendered files")
	flag.Parse()

	if *configFile == "" || *templateDir == "" || *outputDir == "" {
		log.Fatal("All flags required: -config, -template-dir, -output-dir")
	}

	results, err := renderAllTemplates(*configFile, *templateDir, *outputDir)
	if err != nil {
		log.Fatal(err)
	}

	// Print results
	for _, result := range results {
		fmt.Printf("✓ Rendered %s\n", result.TemplateName)
	}
	fmt.Printf("\nSuccessfully rendered %d workflow(s)\n", len(results))
}
