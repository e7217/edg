package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadFromFile_Success tests loading a valid YAML template
func TestLoadFromFile_Success(t *testing.T) {
	loader := NewTemplateLoader()

	err := loader.LoadFromFile("testdata/valid_template.yaml")
	require.NoError(t, err)

	// Verify template was loaded
	template := loader.Get("test-sensor")
	require.NotNil(t, template)
	assert.Equal(t, "test-sensor", template.Name)
	assert.Len(t, template.Resources, 3)

	// Verify resources
	assert.Equal(t, "temperature", template.Resources[0].Name)
	assert.Equal(t, ValueTypeNumber, template.Resources[0].ValueType)
	assert.Equal(t, "status", template.Resources[1].Name)
	assert.Equal(t, ValueTypeText, template.Resources[1].ValueType)
	assert.Equal(t, "enabled", template.Resources[2].Name)
	assert.Equal(t, ValueTypeFlag, template.Resources[2].ValueType)
}

// TestLoadFromFile_InvalidYAML tests handling of malformed YAML
func TestLoadFromFile_InvalidYAML(t *testing.T) {
	loader := NewTemplateLoader()

	// Create temp file with invalid YAML
	tmpDir := t.TempDir()
	invalidFile := filepath.Join(tmpDir, "invalid.yaml")
	err := os.WriteFile(invalidFile, []byte("{ invalid yaml ["), 0644)
	require.NoError(t, err)

	err = loader.LoadFromFile(invalidFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse YAML")
}

// TestLoadFromFile_MissingName tests template without required name field
func TestLoadFromFile_MissingName(t *testing.T) {
	loader := NewTemplateLoader()

	err := loader.LoadFromFile("testdata/invalid_template.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "template name is missing")
}

// TestLoadFromDir_FailsOnInvalidFile tests that LoadFromDir returns error when directory contains invalid template
func TestLoadFromDir_FailsOnInvalidFile(t *testing.T) {
	loader := NewTemplateLoader()

	// LoadFromDir returns error if any file fails to load
	// So we expect an error because invalid_template.yaml has no name
	err := loader.LoadFromDir("testdata")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "template name is missing")
}

// TestValidateAssetData_Success tests successful data validation
func TestValidateAssetData_Success(t *testing.T) {
	loader := NewTemplateLoader()
	err := loader.LoadFromFile("testdata/valid_template.yaml")
	require.NoError(t, err)

	// Create valid asset data matching template
	tempValue := 25.5
	statusValue := "normal"
	enabledValue := true

	data := &AssetData{
		AssetID: "sensor-001",
		Values: []TagValue{
			{Name: "temperature", Number: &tempValue},
			{Name: "status", Text: &statusValue},
			{Name: "enabled", Flag: &enabledValue},
		},
	}

	err = loader.ValidateAssetData("test-sensor", data)
	assert.NoError(t, err)
}

// TestValidateAssetData_InvalidType tests type mismatch detection
func TestValidateAssetData_InvalidType(t *testing.T) {
	loader := NewTemplateLoader()
	err := loader.LoadFromFile("testdata/valid_template.yaml")
	require.NoError(t, err)

	// Create asset data with wrong type (temperature should be NUMBER, not TEXT)
	wrongValue := "not-a-number"
	data := &AssetData{
		AssetID: "sensor-001",
		Values: []TagValue{
			{Name: "temperature", Text: &wrongValue}, // Wrong type
		},
	}

	err = loader.ValidateAssetData("test-sensor", data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be NUMBER type")
}
