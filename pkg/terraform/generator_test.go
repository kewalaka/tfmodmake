package terraform

import (
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"camelCase", "camel_case"},
		{"PascalCase", "pascal_case"},
		{"snake_case", "snake_case"},
		{"HTTPClient", "http_client"},
		{"simple", "simple"},
		{"agentPoolProfiles", "agent_pool_profiles"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toSnakeCase(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGenerate(t *testing.T) {
	// Create a temporary directory for output
	tmpDir := t.TempDir()
	
	// Change working directory to temp dir
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)
	
	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Setup schema
	schema := &openapi3.Schema{
		Type: &openapi3.Types{"object"},
		Properties: map[string]*openapi3.SchemaRef{
			"location": {
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"string"},
					Description: "Resource location",
				},
			},
			"properties": {
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"object"},
					Properties: map[string]*openapi3.SchemaRef{
						"readOnlyProp": {
							Value: &openapi3.Schema{
								Type: &openapi3.Types{"string"},
								ReadOnly: true,
							},
						},
						"writableProp": {
							Value: &openapi3.Schema{
								Type: &openapi3.Types{"string"},
							},
						},
					},
				},
			},
		},
	}

	// Run Generate
	err = Generate(schema, "testResource", "test_local")
	require.NoError(t, err)

	// Check variables.tf
	varsContent, err := os.ReadFile("variables.tf")
	require.NoError(t, err)
	varsStr := string(varsContent)
	
	assert.Contains(t, varsStr, `variable "location"`)
	assert.Contains(t, varsStr, `variable "properties"`)
	assert.NotContains(t, varsStr, `variable "read_only_prop"`) // Should be filtered out

	// Check locals.tf
	localsContent, err := os.ReadFile("locals.tf")
	require.NoError(t, err)
	localsStr := string(localsContent)
	
	assert.Contains(t, localsStr, "locals {")
	assert.Contains(t, localsStr, "test_local = {")
	assert.Contains(t, localsStr, "location = var.location")
	assert.Contains(t, localsStr, "writableProp = var.properties.writable_prop")
	assert.NotContains(t, localsStr, "readOnlyProp")
}

func TestMapType(t *testing.T) {
	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   string
	}{
		{
			name:   "string",
			schema: &openapi3.Schema{Type: &openapi3.Types{"string"}},
			want:   "string",
		},
		{
			name:   "integer",
			schema: &openapi3.Schema{Type: &openapi3.Types{"integer"}},
			want:   "number",
		},
		{
			name:   "boolean",
			schema: &openapi3.Schema{Type: &openapi3.Types{"boolean"}},
			want:   "bool",
		},
		{
			name: "array of strings",
			schema: &openapi3.Schema{
				Type: &openapi3.Types{"array"},
				Items: &openapi3.SchemaRef{
					Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
				},
			},
			want: "list(string)",
		},
		{
			name: "object",
			schema: &openapi3.Schema{
				Type: &openapi3.Types{"object"},
				Properties: map[string]*openapi3.SchemaRef{
					"prop1": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				},
			},
			// Note: mapType output depends on sorting and formatting, so we might need loose matching or exact string
			// The current implementation returns formatted string
			want: "object({\n    prop1 = optional(string)\n  })",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapType(tt.schema)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildNestedDescription(t *testing.T) {
	schema := &openapi3.Schema{
		Properties: map[string]*openapi3.SchemaRef{
			"prop1": {
				Value: &openapi3.Schema{
					Description: "Description 1",
				},
			},
			"nested": {
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"object"},
					Description: "Nested object",
					Properties: map[string]*openapi3.SchemaRef{
						"child": {
							Value: &openapi3.Schema{
								Description: "Child description",
							},
						},
					},
				},
			},
		},
	}

	got := buildNestedDescription(schema, "")
	assert.Contains(t, got, "- `prop1` - Description 1")
	assert.Contains(t, got, "- `nested` - Nested object")
	assert.Contains(t, got, "  - `child` - Child description")
}
