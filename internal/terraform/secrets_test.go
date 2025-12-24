package terraform

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
)

func TestIsSecretField(t *testing.T) {
	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   bool
	}{
		{
			name:   "nil schema",
			schema: nil,
			want:   false,
		},
		{
			name: "x-ms-secret true",
			schema: &openapi3.Schema{
				Type: &openapi3.Types{"string"},
				Extensions: map[string]any{
					"x-ms-secret": true,
				},
			},
			want: true,
		},
		{
			name: "x-ms-secret false",
			schema: &openapi3.Schema{
				Type: &openapi3.Types{"string"},
				Extensions: map[string]any{
					"x-ms-secret": false,
				},
			},
			want: false,
		},
		{
			name: "x-ms-secret non-boolean",
			schema: &openapi3.Schema{
				Type: &openapi3.Types{"string"},
				Extensions: map[string]any{
					"x-ms-secret": "true",
				},
			},
			want: false,
		},
		{
			name: "writeOnly true",
			schema: &openapi3.Schema{
				Type:      &openapi3.Types{"string"},
				WriteOnly: true,
			},
			want: true,
		},
		{
			name: "writeOnly false",
			schema: &openapi3.Schema{
				Type:      &openapi3.Types{"string"},
				WriteOnly: false,
			},
			want: false,
		},
		{
			name: "description with 'never be returned'",
			schema: &openapi3.Schema{
				Type:        &openapi3.Types{"string"},
				Description: "This value will never be returned from the service.",
			},
			want: true,
		},
		{
			name: "description with 'never be returned' (case insensitive)",
			schema: &openapi3.Schema{
				Type:        &openapi3.Types{"string"},
				Description: "This value will NEVER BE RETURNED from the service.",
			},
			want: true,
		},
		{
			name: "description with 'never be returned' in mixed case",
			schema: &openapi3.Schema{
				Type:        &openapi3.Types{"string"},
				Description: "This secret Never Be Returned by the API.",
			},
			want: true,
		},
		{
			name: "description without secret keywords",
			schema: &openapi3.Schema{
				Type:        &openapi3.Types{"string"},
				Description: "A normal field description.",
			},
			want: false,
		},
		{
			name: "empty description",
			schema: &openapi3.Schema{
				Type:        &openapi3.Types{"string"},
				Description: "",
			},
			want: false,
		},
		{
			name: "no extensions",
			schema: &openapi3.Schema{
				Type: &openapi3.Types{"string"},
			},
			want: false,
		},
		{
			name: "writeOnly true overrides other conditions",
			schema: &openapi3.Schema{
				Type:        &openapi3.Types{"string"},
				WriteOnly:   true,
				Description: "A normal field",
			},
			want: true,
		},
		{
			name: "description-based detection with x-ms-secret false",
			schema: &openapi3.Schema{
				Type:        &openapi3.Types{"string"},
				Description: "This value will never be returned from the service.",
				Extensions: map[string]any{
					"x-ms-secret": false,
				},
			},
			want: true,
		},
		{
			name: "all secret indicators present",
			schema: &openapi3.Schema{
				Type:        &openapi3.Types{"string"},
				WriteOnly:   true,
				Description: "This value will never be returned from the service.",
				Extensions: map[string]any{
					"x-ms-secret": true,
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSecretField(tt.schema)
			assert.Equal(t, tt.want, got, "isSecretField() result mismatch")
		})
	}
}
