package actionindex

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ActionVersion represents a GitHub Action version with SHA
type ActionVersion struct {
	Version string `yaml:"version"`
	SHA     string `yaml:"sha"`
}

// ActionIndex contains all tracked GitHub Actions versions
type ActionIndex struct {
	Actions map[string]ActionVersion `yaml:"actions"`
}

// Load reads and parses the action versions index from a file
func Load(path string) (*ActionIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read action index: %w", err)
	}

	var index ActionIndex
	if err := yaml.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse action index: %w", err)
	}

	return &index, nil
}
