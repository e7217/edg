package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// TemplateLoader is an in-memory cache of asset templates. When backed by a
// Store (NewTemplateLoaderWithStore), writes are persisted to SQLite while the
// cache remains the authoritative read path, so the relation-create constraint
// hot path stays in-memory.
type TemplateLoader struct {
	mu        sync.RWMutex
	store     *Store
	templates map[string]*AssetTemplate
}

// NewTemplateLoader creates an in-memory-only loader (no persistence).
func NewTemplateLoader() *TemplateLoader {
	return &TemplateLoader{
		templates: make(map[string]*AssetTemplate),
	}
}

// NewTemplateLoaderWithStore creates a loader backed by the given Store and
// populates its cache from the database.
func NewTemplateLoaderWithStore(store *Store) (*TemplateLoader, error) {
	l := &TemplateLoader{
		store:     store,
		templates: make(map[string]*AssetTemplate),
	}
	if err := l.reloadFromStore(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *TemplateLoader) reloadFromStore() error {
	if l.store == nil {
		return nil
	}
	templates, err := l.store.ListTemplates()
	if err != nil {
		return fmt.Errorf("failed to load templates from store: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.templates = make(map[string]*AssetTemplate, len(templates))
	for _, t := range templates {
		l.templates[t.Name] = t
	}
	return nil
}

// Upsert persists a template (when store-backed) and updates the cache.
func (l *TemplateLoader) Upsert(template *AssetTemplate) error {
	if template == nil || template.Name == "" {
		return fmt.Errorf("template name is required")
	}
	if l.store != nil {
		if err := l.store.UpsertTemplate(template); err != nil {
			return err
		}
	}
	l.mu.Lock()
	l.templates[template.Name] = template
	l.mu.Unlock()
	return nil
}

// Delete removes a template from the store (when store-backed) and the cache.
func (l *TemplateLoader) Delete(name string) error {
	if l.store != nil {
		if err := l.store.DeleteTemplate(name); err != nil {
			return err
		}
	}
	l.mu.Lock()
	delete(l.templates, name)
	l.mu.Unlock()
	return nil
}

// LoadFromDir imports all YAML templates from a directory (persisting to the
// store when store-backed) and validates cross-template constraints.
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

	return validateTemplateConstraints(l)
}

// LoadFromFile imports a single YAML template file.
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

	return l.Upsert(&template)
}

// ExportToDir writes each template to dir/<name>.yaml (round-trips with import).
func (l *TemplateLoader) ExportToDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create export directory: %w", err)
	}
	for _, t := range l.List() {
		data, err := yaml.Marshal(t)
		if err != nil {
			return fmt.Errorf("failed to encode template %s: %w", t.Name, err)
		}
		path := filepath.Join(dir, t.Name+".yaml")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("failed to write template %s: %w", t.Name, err)
		}
	}
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
