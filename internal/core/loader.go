package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// TemplateLoader loads asset templates from YAML files
type TemplateLoader struct {
	mu        sync.RWMutex
	templates map[string]*AssetTemplate
}

// NewTemplateLoader creates a new loader
func NewTemplateLoader() *TemplateLoader {
	return &TemplateLoader{
		templates: make(map[string]*AssetTemplate),
	}
}

// LoadFromDir loads all YAML templates from a directory
func (l *TemplateLoader) LoadFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		if err := l.LoadFromFile(path); err != nil {
			return fmt.Errorf("failed to load template (%s): %w", path, err)
		}
	}

	return nil
}

// LoadFromFile loads a template from a single YAML file
func (l *TemplateLoader) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	var template AssetTemplate
	if err := yaml.Unmarshal(data, &template); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	if template.Name == "" {
		return fmt.Errorf("template name is missing: %s", path)
	}

	l.mu.Lock()
	l.templates[template.Name] = &template
	l.mu.Unlock()

	return nil
}

// Get retrieves a template by name
func (l *TemplateLoader) Get(name string) *AssetTemplate {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.templates[name]
}

// List returns all templates
func (l *TemplateLoader) List() []*AssetTemplate {
	l.mu.RLock()
	defer l.mu.RUnlock()

	list := make([]*AssetTemplate, 0, len(l.templates))
	for _, t := range l.templates {
		list = append(list, t)
	}
	return list
}

// Exists checks if a template exists
func (l *TemplateLoader) Exists(name string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.templates[name]
	return ok
}

// Count returns the number of loaded templates
func (l *TemplateLoader) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.templates)
}

// ValidateAssetData validates asset data against a template
func (l *TemplateLoader) ValidateAssetData(templateName string, data *AssetData) error {
	template := l.Get(templateName)
	if template == nil {
		// skip validation if template not found (optional validation)
		return nil
	}

	// build resource map
	resourceMap := make(map[string]*AssetResource)
	for i := range template.Resources {
		resourceMap[template.Resources[i].Name] = &template.Resources[i]
	}

	// validate each TagValue
	for _, tv := range data.Values {
		res, ok := resourceMap[tv.Name]
		if !ok {
			// undefined tag (warning only, not an error)
			continue
		}

		// validate value type
		switch res.ValueType {
		case ValueTypeNumber:
			if tv.Number == nil {
				return fmt.Errorf("tag '%s' must be NUMBER type", tv.Name)
			}
		case ValueTypeText:
			if tv.Text == nil {
				return fmt.Errorf("tag '%s' must be TEXT type", tv.Name)
			}
		case ValueTypeFlag:
			if tv.Flag == nil {
				return fmt.Errorf("tag '%s' must be FLAG type", tv.Name)
			}
		}
	}

	return nil
}
