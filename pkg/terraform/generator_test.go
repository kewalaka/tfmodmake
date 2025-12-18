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
		{"AdminGroupObjectIDs", "admin_group_object_ids"},
		{"HTTPServer", "http_server"},
		{"JSONList", "json_list"},
		{"MyAPIs", "my_apis"},
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
					Type:        &openapi3.Types{"string"},
					Description: "Resource location",
				},
			},
			"properties": {
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"object"},
					Properties: map[string]*openapi3.SchemaRef{
						"readOnlyProp": {
							Value: &openapi3.Schema{
								Type:     &openapi3.Types{"string"},
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
	supportsTags := SupportsTags(schema)

	err = Generate(schema, "testResource", "test_local", supportsTags)
	require.NoError(t, err)

	// Check variables.tf
	varsContent, err := os.ReadFile("variables.tf")
	require.NoError(t, err)
	varsStr := string(varsContent)

	assert.Contains(t, varsStr, `variable "name"`)
	assert.Contains(t, varsStr, `variable "parent_id"`)
	assert.NotContains(t, varsStr, `variable "tags"`)
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

	mainContent, err := os.ReadFile("main.tf")
	require.NoError(t, err)
	mainStr := string(mainContent)

	assert.Contains(t, mainStr, `resource "azapi_resource" "this" {`)
	assert.Contains(t, mainStr, `type      = "testResource@apiVersion"`)
	assert.Contains(t, mainStr, "properties = local.test_local")
	assert.NotContains(t, mainStr, "tags = var.tags")

	outputsContent, err := os.ReadFile("outputs.tf")
	require.NoError(t, err)
	outputsStr := string(outputsContent)

	assert.Contains(t, outputsStr, `output "resource_id"`)
	assert.Contains(t, outputsStr, `output "name"`)
	assert.Contains(t, outputsStr, "azapi_resource.this.id")
	assert.Contains(t, outputsStr, "azapi_resource.this.name")
}

func TestGenerate_IncludesAdditionalPropertiesDescription(t *testing.T) {
	tmpDir := t.TempDir()

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	schema := &openapi3.Schema{
		Type: &openapi3.Types{"object"},
		Properties: map[string]*openapi3.SchemaRef{
			"kubeDnsOverrides": {
				Value: &openapi3.Schema{
					Type:        &openapi3.Types{"object"},
					Description: "Overrides for kube DNS queries.",
					AdditionalProperties: openapi3.AdditionalProperties{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Type: &openapi3.Types{"object"},
								Properties: map[string]*openapi3.SchemaRef{
									"queryLogging": {
										Value: &openapi3.Schema{
											Type:        &openapi3.Types{"string"},
											Description: "Enable query logging.",
										},
									},
									"maxConcurrent": {
										Value: &openapi3.Schema{
											Type:        &openapi3.Types{"integer"},
											Description: "Maximum concurrent queries.",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	err = Generate(schema, "testResource", "local_map", false)
	require.NoError(t, err)

	varsContent, err := os.ReadFile("variables.tf")
	require.NoError(t, err)
	varsStr := string(varsContent)

	assert.Contains(t, varsStr, `variable "kube_dns_overrides" {`)
	assert.Contains(t, varsStr, "description = <<-DESCRIPTION")
	assert.Contains(t, varsStr, "Overrides for kube DNS queries.")
	assert.Contains(t, varsStr, "Map values:")
	assert.Contains(t, varsStr, "- `max_concurrent` - Maximum concurrent queries.")
	assert.Contains(t, varsStr, "- `query_logging` - Enable query logging.")
}

func TestGenerate_WithTagsSupport(t *testing.T) {
	tmpDir := t.TempDir()

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	schema := &openapi3.Schema{
		Type: &openapi3.Types{"object"},
		Properties: map[string]*openapi3.SchemaRef{
			"location": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
			"tags": {
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"object"},
					AdditionalProperties: openapi3.AdditionalProperties{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
						},
					},
				},
			},
		},
	}

	err = Generate(schema, "testResource", "resource_body", true)
	require.NoError(t, err)

	varsContent, err := os.ReadFile("variables.tf")
	require.NoError(t, err)
	varsStr := string(varsContent)
	assert.Contains(t, varsStr, `variable "tags"`)

	mainContent, err := os.ReadFile("main.tf")
	require.NoError(t, err)
	mainStr := string(mainContent)
	assert.Contains(t, mainStr, "tags = var.tags")
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
		{
			name: "object with additionalProperties object",
			schema: &openapi3.Schema{
				Type: &openapi3.Types{"object"},
				AdditionalProperties: openapi3.AdditionalProperties{
					Schema: &openapi3.SchemaRef{
						Value: &openapi3.Schema{
							Type:     &openapi3.Types{"object"},
							Required: []string{"queryLogging"},
							Properties: map[string]*openapi3.SchemaRef{
								"queryLogging":  {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
								"maxConcurrent": {Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}}},
							},
						},
					},
				},
			},
			want: "map(object({\n    max_concurrent = optional(number)\n    query_logging = string\n  }))",
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
					Type:        &openapi3.Types{"object"},
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

func TestConstructValue_MapAdditionalPropertiesObject(t *testing.T) {
	schema := &openapi3.Schema{
		Type: &openapi3.Types{"object"},
		AdditionalProperties: openapi3.AdditionalProperties{
			Schema: &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"object"},
					Properties: map[string]*openapi3.SchemaRef{
						"queryLogging": {
							Value: &openapi3.Schema{
								Type: &openapi3.Types{"string"},
							},
						},
						"maxConcurrent": {
							Value: &openapi3.Schema{
								Type: &openapi3.Types{"integer"},
							},
						},
					},
				},
			},
		},
	}

	got := constructValue(schema, "var.kube_dns_overrides", false)

	expected := "var.kube_dns_overrides == null ? null : { for k, value in var.kube_dns_overrides : k => value == null ? null : {\nmaxConcurrent = value.max_concurrent\nqueryLogging = value.query_logging\n} }"
	assert.Equal(t, expected, got)
}
