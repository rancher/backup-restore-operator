package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGoConfig_UnmarshalYAML_SimpleFormat(t *testing.T) {
	tests := []struct {
		name            string
		yaml            string
		expectedVersion string
		expectedCIImage string
	}{
		{
			name:            "simple major.minor",
			yaml:            "go: 1.25",
			expectedVersion: "1.25",
			expectedCIImage: "",
		},
		{
			name:            "simple with patch",
			yaml:            "go: 1.25.0",
			expectedVersion: "1.25.0",
			expectedCIImage: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type testConfig struct {
				Go GoConfig `yaml:"go"`
			}
			var cfg testConfig
			err := yaml.Unmarshal([]byte(tt.yaml), &cfg)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedVersion, cfg.Go.Version)
			assert.Equal(t, tt.expectedCIImage, cfg.Go.CIImage)
		})
	}
}

func TestGoConfig_UnmarshalYAML_ExplicitFormat(t *testing.T) {
	yamlStr := `
go:
  version: 1.25.0
  ci_image: go1.25
`
	type testConfig struct {
		Go GoConfig `yaml:"go"`
	}
	var cfg testConfig
	err := yaml.Unmarshal([]byte(yamlStr), &cfg)
	require.NoError(t, err)
	assert.Equal(t, "1.25.0", cfg.Go.Version)
	assert.Equal(t, "go1.25", cfg.Go.CIImage)
}

func TestGoConfig_GetVersion(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		expected string
	}{
		{
			name:     "major.minor gets patch",
			version:  "1.25",
			expected: "1.25.0",
		},
		{
			name:     "full version unchanged",
			version:  "1.25.3",
			expected: "1.25.3",
		},
		{
			name:     "empty version",
			version:  "",
			expected: "",
		},
		{
			name:     "major only",
			version:  "1",
			expected: "1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gc := GoConfig{Version: tt.version}
			assert.Equal(t, tt.expected, gc.GetVersion())
		})
	}
}

func TestGoConfig_GetCIImage(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		ciImage  string
		expected string
	}{
		{
			name:     "explicit ci_image takes precedence",
			version:  "1.25.0",
			ciImage:  "go1.25",
			expected: "go1.25",
		},
		{
			name:     "derive from major.minor",
			version:  "1.25",
			ciImage:  "",
			expected: "go1.25",
		},
		{
			name:     "derive from full version",
			version:  "1.25.3",
			ciImage:  "",
			expected: "go1.25",
		},
		{
			name:     "custom ci_image",
			version:  "1.25.0",
			ciImage:  "custom-go-image",
			expected: "custom-go-image",
		},
		{
			name:     "empty version",
			version:  "",
			ciImage:  "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gc := GoConfig{
				Version: tt.version,
				CIImage: tt.ciImage,
			}
			assert.Equal(t, tt.expected, gc.GetCIImage())
		})
	}
}

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-config.yaml")

	configContent := `
branch: release/v9.x
go: 1.25
k3s_versions:
  - v1.32.13-k3s1
  - v1.34.6-k3s1
automation_core_ref: automation-core
workflows:
  ci: true
  release: true
  head-builds: true
  fossa: true
description: "Test branch"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err, "Failed to write test config")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)

	assert.Equal(t, "release/v9.x", cfg.Branch)
	assert.Equal(t, "1.25", cfg.Go.Version)
	assert.Len(t, cfg.K3SVersions, 2)
	assert.Equal(t, "automation-core", cfg.AutomationCoreRef)
}

func TestTemplateFunctions_jsonArray(t *testing.T) {
	cfg := &Config{
		K3SVersions: []string{"v1.32.13-k3s1", "v1.34.6-k3s1"},
	}

	funcs := templateFuncs(cfg)
	jsonArrayFn := funcs["jsonArray"].(func([]string) string)

	result := jsonArrayFn(cfg.K3SVersions)
	assert.Equal(t, `["v1.32.13-k3s1", "v1.34.6-k3s1"]`, result)
}

func TestTemplateFunctions_goVersion(t *testing.T) {
	tests := []struct {
		name     string
		goConfig GoConfig
		expected string
	}{
		{
			name:     "simple format",
			goConfig: GoConfig{Version: "1.25"},
			expected: "1.25.0",
		},
		{
			name:     "explicit format",
			goConfig: GoConfig{Version: "1.25.3"},
			expected: "1.25.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Go: tt.goConfig}
			funcs := templateFuncs(cfg)
			goVersionFn := funcs["goVersion"].(func() string)

			assert.Equal(t, tt.expected, goVersionFn())
		})
	}
}

func TestTemplateFunctions_goCIImage(t *testing.T) {
	tests := []struct {
		name     string
		goConfig GoConfig
		expected string
	}{
		{
			name:     "simple format",
			goConfig: GoConfig{Version: "1.25"},
			expected: "go1.25",
		},
		{
			name:     "explicit ci_image",
			goConfig: GoConfig{Version: "1.25.0", CIImage: "custom-go"},
			expected: "custom-go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Go: tt.goConfig}
			funcs := templateFuncs(cfg)
			goCIImageFn := funcs["goCIImage"].(func() string)

			assert.Equal(t, tt.expected, goCIImageFn())
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	templatePath := filepath.Join(tmpDir, "test.yaml.tmpl")

	templateContent := `name: Test Workflow
go_version: [[goVersion]]
ci_image: [[goCIImage]]
k3s_versions: '[[jsonArray .K3SVersions]]'
automation_ref: [[.AutomationCoreRef]]
`
	err := os.WriteFile(templatePath, []byte(templateContent), 0644)
	require.NoError(t, err, "Failed to write test template")

	cfg := &Config{
		Branch:            "release/v9.x",
		Go:                GoConfig{Version: "1.25"},
		K3SVersions:       []string{"v1.32.13-k3s1"},
		AutomationCoreRef: "automation-core",
	}

	rendered, err := renderTemplate(templatePath, cfg)
	require.NoError(t, err)

	assert.Contains(t, rendered, "go_version: 1.25.0")
	assert.Contains(t, rendered, "ci_image: go1.25")
	assert.Contains(t, rendered, `k3s_versions: '["v1.32.13-k3s1"]'`)
	assert.Contains(t, rendered, "automation_ref: automation-core")
}

func TestRenderTemplate_GitHubActionsPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	templatePath := filepath.Join(tmpDir, "test.yaml.tmpl")

	templateContent := `name: Test
token: ${{ secrets.GITHUB_TOKEN }}
ref: ${{ github.ref_name }}
go_image: [[goCIImage]]
`
	err := os.WriteFile(templatePath, []byte(templateContent), 0644)
	require.NoError(t, err, "Failed to write test template")

	cfg := &Config{
		Go: GoConfig{Version: "1.25"},
	}

	rendered, err := renderTemplate(templatePath, cfg)
	require.NoError(t, err)

	// GitHub Actions syntax should be preserved
	assert.Contains(t, rendered, "token: ${{ secrets.GITHUB_TOKEN }}")
	assert.Contains(t, rendered, "ref: ${{ github.ref_name }}")
	// Template syntax should be rendered
	assert.Contains(t, rendered, "go_image: go1.25")
}

func TestRenderTemplate_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	templatePath := filepath.Join(tmpDir, "invalid.yaml.tmpl")

	templateContent := `name: Test
invalid yaml here: [unclosed bracket
`
	err := os.WriteFile(templatePath, []byte(templateContent), 0644)
	require.NoError(t, err, "Failed to write test template")

	cfg := &Config{}

	_, err = renderTemplate(templatePath, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid YAML output")
}

func TestLoadConfig_ExplicitGoFormat(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-explicit.yaml")

	configContent := `
branch: test
go:
  version: 1.26.0
  ci_image: go1.26
k3s_versions:
  - v1.34.5-k3s1
automation_core_ref: automation-core
workflows:
  ci: true
description: "Test explicit Go config"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err, "Failed to write test config")

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)

	assert.Equal(t, "1.26.0", cfg.Go.Version)
	assert.Equal(t, "go1.26", cfg.Go.CIImage)
	assert.Equal(t, "1.26.0", cfg.Go.GetVersion())
	assert.Equal(t, "go1.26", cfg.Go.GetCIImage())
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/config.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read config")
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")

	invalidContent := `
branch: test
invalid yaml: [unclosed
`
	err := os.WriteFile(configPath, []byte(invalidContent), 0644)
	require.NoError(t, err)

	_, err = loadConfig(configPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse config")
}

func TestGoConfig_UnmarshalYAML_InvalidFormat(t *testing.T) {
	// Test invalid YAML that can't be unmarshaled as string or struct
	yamlStr := `go: [1, 2, 3]` // Array is not valid for GoConfig
	type testConfig struct {
		Go GoConfig `yaml:"go"`
	}
	var cfg testConfig
	err := yaml.Unmarshal([]byte(yamlStr), &cfg)
	assert.Error(t, err)
}

func TestGoConfig_GetCIImage_SinglePart(t *testing.T) {
	// Test version with only one part (edge case)
	gc := GoConfig{Version: "1"}
	assert.Equal(t, "go1", gc.GetCIImage())
}

func TestRenderTemplate_TemplateNotFound(t *testing.T) {
	cfg := &Config{
		Go: GoConfig{Version: "1.25"},
	}

	_, err := renderTemplate("/nonexistent/template.tmpl", cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse template")
}

func TestRenderTemplate_TemplateExecutionError(t *testing.T) {
	tmpDir := t.TempDir()
	templatePath := filepath.Join(tmpDir, "bad-template.tmpl")

	// Template with undefined variable
	templateContent := `name: [[.UndefinedField]]`
	err := os.WriteFile(templatePath, []byte(templateContent), 0644)
	require.NoError(t, err)

	cfg := &Config{
		Go: GoConfig{Version: "1.25"},
	}

	_, err = renderTemplate(templatePath, cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "execute template")
}

func TestTemplateFunctions_jsonArray_EmptyArray(t *testing.T) {
	cfg := &Config{
		K3SVersions: []string{},
	}

	funcs := templateFuncs(cfg)
	jsonArrayFn := funcs["jsonArray"].(func([]string) string)

	result := jsonArrayFn(cfg.K3SVersions)
	assert.Equal(t, `[]`, result)
}

func TestTemplateFunctions_jsonArray_SingleItem(t *testing.T) {
	cfg := &Config{
		K3SVersions: []string{"v1.32.13-k3s1"},
	}

	funcs := templateFuncs(cfg)
	jsonArrayFn := funcs["jsonArray"].(func([]string) string)

	result := jsonArrayFn(cfg.K3SVersions)
	assert.Equal(t, `["v1.32.13-k3s1"]`, result)
}

func TestGoConfig_GetVersion_ThreeParts(t *testing.T) {
	gc := GoConfig{Version: "1.25.3"}
	assert.Equal(t, "1.25.3", gc.GetVersion())
}

func TestGoConfig_GetVersion_FourParts(t *testing.T) {
	// Edge case: version with more than 3 parts
	gc := GoConfig{Version: "1.25.3.4"}
	assert.Equal(t, "1.25.3.4", gc.GetVersion())
}

func TestRenderAllTemplates_Success(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config file
	configDir := filepath.Join(tmpDir, "config")
	err := os.MkdirAll(configDir, 0755)
	require.NoError(t, err)

	configPath := filepath.Join(configDir, "test.yaml")
	configContent := `
branch: test
go: 1.25
k3s_versions:
  - v1.32.13-k3s1
automation_core_ref: automation-core
workflows:
  ci: true
description: "Test"
`
	err = os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Create template files
	templateDir := filepath.Join(tmpDir, "templates")
	err = os.MkdirAll(templateDir, 0755)
	require.NoError(t, err)

	template1 := filepath.Join(templateDir, "workflow1.yaml.tmpl")
	template1Content := `name: Workflow 1
go_version: [[goVersion]]
`
	err = os.WriteFile(template1, []byte(template1Content), 0644)
	require.NoError(t, err)

	template2 := filepath.Join(templateDir, "workflow2.yaml.tmpl")
	template2Content := `name: Workflow 2
ci_image: [[goCIImage]]
`
	err = os.WriteFile(template2, []byte(template2Content), 0644)
	require.NoError(t, err)

	// Render
	outputDir := filepath.Join(tmpDir, "output")
	results, err := renderAllTemplates(configPath, templateDir, outputDir)
	require.NoError(t, err)

	// Verify results
	assert.Len(t, results, 2)

	// Check output files exist
	output1 := filepath.Join(outputDir, "workflow1.yaml")
	output2 := filepath.Join(outputDir, "workflow2.yaml")

	assert.FileExists(t, output1)
	assert.FileExists(t, output2)

	// Verify content
	content1, err := os.ReadFile(output1)
	require.NoError(t, err)
	assert.Contains(t, string(content1), "go_version: 1.25.0")

	content2, err := os.ReadFile(output2)
	require.NoError(t, err)
	assert.Contains(t, string(content2), "ci_image: go1.25")
}

func TestRenderAllTemplates_ConfigNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := renderAllTemplates(
		"/nonexistent/config.yaml",
		tmpDir,
		tmpDir,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "load config")
}

func TestRenderAllTemplates_NoTemplatesFound(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config file
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `
branch: test
go: 1.25
k3s_versions:
  - v1.32.13-k3s1
automation_core_ref: automation-core
workflows:
  ci: true
description: "Test"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Empty template dir
	templateDir := filepath.Join(tmpDir, "templates")
	err = os.MkdirAll(templateDir, 0755)
	require.NoError(t, err)

	outputDir := filepath.Join(tmpDir, "output")
	_, err = renderAllTemplates(configPath, templateDir, outputDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no .tmpl files found")
}

func TestRenderAllTemplates_TemplateRenderError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config file
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `
branch: test
go: 1.25
k3s_versions:
  - v1.32.13-k3s1
automation_core_ref: automation-core
workflows:
  ci: true
description: "Test"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Create template with error
	templateDir := filepath.Join(tmpDir, "templates")
	err = os.MkdirAll(templateDir, 0755)
	require.NoError(t, err)

	badTemplate := filepath.Join(templateDir, "bad.yaml.tmpl")
	badContent := `name: [[.UndefinedField]]` // Will cause execution error
	err = os.WriteFile(badTemplate, []byte(badContent), 0644)
	require.NoError(t, err)

	outputDir := filepath.Join(tmpDir, "output")
	_, err = renderAllTemplates(configPath, templateDir, outputDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render")
}

func TestRenderAllTemplates_InvalidOutputDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config file
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `
branch: test
go: 1.25
k3s_versions:
  - v1.32.13-k3s1
automation_core_ref: automation-core
workflows:
  ci: true
description: "Test"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Create template
	templateDir := filepath.Join(tmpDir, "templates")
	err = os.MkdirAll(templateDir, 0755)
	require.NoError(t, err)

	template := filepath.Join(templateDir, "test.yaml.tmpl")
	err = os.WriteFile(template, []byte("name: test"), 0644)
	require.NoError(t, err)

	// Try to use a file as output dir (should fail)
	invalidOutputDir := filepath.Join(tmpDir, "file-not-dir")
	err = os.WriteFile(invalidOutputDir, []byte("content"), 0644)
	require.NoError(t, err)

	_, err = renderAllTemplates(configPath, templateDir, invalidOutputDir)
	// This should fail when trying to create output dir or write file
	assert.Error(t, err)
}

func TestRenderAllTemplates_MultipleTemplates(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `
branch: test
go: 1.25
k3s_versions:
  - v1.32.13-k3s1
  - v1.34.6-k3s1
automation_core_ref: automation-core
workflows:
  ci: true
description: "Test"
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err)

	// Create 3 templates
	templateDir := filepath.Join(tmpDir, "templates")
	err = os.MkdirAll(templateDir, 0755)
	require.NoError(t, err)

	for i := 1; i <= 3; i++ {
		templatePath := filepath.Join(templateDir, fmt.Sprintf("workflow%d.yaml.tmpl", i))
		content := fmt.Sprintf("name: Workflow %d\ngo: [[goVersion]]\n", i)
		err = os.WriteFile(templatePath, []byte(content), 0644)
		require.NoError(t, err)
	}

	outputDir := filepath.Join(tmpDir, "output")
	results, err := renderAllTemplates(configPath, templateDir, outputDir)
	require.NoError(t, err)

	assert.Len(t, results, 3)
	assert.Equal(t, "workflow1.yaml", results[0].TemplateName)
	assert.Equal(t, "workflow2.yaml", results[1].TemplateName)
	assert.Equal(t, "workflow3.yaml", results[2].TemplateName)
}

// Note: The filepath.Glob error path in renderAllTemplates (line ~173) is extremely
// difficult to test in practice. filepath.Glob only returns an error for malformed
// pattern syntax (e.g., invalid escape sequences), but our pattern is constructed
// programmatically and will always be valid. To trigger this error, we would need
// to pass invalid characters in templateDir itself, which os.MkdirAll prevents.
// This error path exists for defensive programming but is unreachable in normal use.
