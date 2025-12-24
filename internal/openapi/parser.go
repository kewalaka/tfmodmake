// Package openapi provides functions to parse OpenAPI specifications and extract resource schemas.
package openapi

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// LoadSpec loads the OpenAPI specification from a file path or URL.
func LoadSpec(path string) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	u, err := url.Parse(path)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return loader.LoadFromURI(u)
	}

	return loader.LoadFromFile(path)
}

// FindResource identifies the schema for the specified resource type.
// It looks for a path containing the resource type and returns the schema
// for the PUT request body.
func FindResource(doc *openapi3.T, resourceType string) (*openapi3.Schema, error) {
	// Normalize resource type for search
	// e.g. Microsoft.ContainerService/managedClusters

	// If the resource type contains a placeholder (e.g. {resourceName}), strip it
	// to match against the path regardless of the parameter name used in the spec.
	searchType := resourceType
	if strings.HasSuffix(searchType, "}") {
		if idx := strings.LastIndex(searchType, "/{"); idx != -1 {
			searchType = searchType[:idx]
		}
	}

	// Strategy: Look for a PUT path that represents an Azure ARM resource instance.
	// Azure ARM instance paths usually look like:
	// - .../providers/Microsoft.ContainerService/managedClusters/{resourceName}
	// - .../providers/Microsoft.KeyVault/vaults/{vaultName}/secrets/{secretName}

	var bestMatchSchema *openapi3.Schema

	for path, pathItem := range doc.Paths.Map() {
		if pathItem.Put == nil {
			continue
		}

		// Prefer matching ARM instance paths by deriving the effective resource type.
		if derivedType, ok := azureARMInstanceResourceTypeFromPath(path); ok {
			if !strings.EqualFold(derivedType, searchType) {
				continue
			}
		} else {
			// Fallback: substring match for specs that don't follow the standard ARM path pattern.
			lowerPath := strings.ToLower(path)
			lowerResourceType := strings.ToLower(searchType)
			idx := strings.Index(lowerPath, lowerResourceType)
			if idx == -1 {
				continue
			}
			if idx > 0 && lowerPath[idx-1] != '/' {
				continue
			}
			suffix := lowerPath[idx+len(lowerResourceType):]
			if suffix != "" && suffix[0] != '/' {
				continue
			}
			segments := 0
			if suffix != "" {
				trimmed := suffix[1:]
				if trimmed != "" {
					segments = strings.Count(trimmed, "/") + 1
				}
			}
			if segments > 1 {
				continue
			}
		}

		var schema *openapi3.Schema

		// Check RequestBody (OpenAPI 3)
		if pathItem.Put.RequestBody != nil && pathItem.Put.RequestBody.Value != nil {
			content := pathItem.Put.RequestBody.Value.Content
			if jsonContent, ok := content["application/json"]; ok {
				if jsonContent.Schema != nil {
					schema = jsonContent.Schema.Value
				}
			}
		}

		// Fallback for Swagger/OpenAPI v2 specs, which model request bodies as
		// a body parameter instead of an OpenAPI v3 RequestBody.
		// Azure REST API specs can still contain these in older/preview specs.
		if schema == nil {
			for _, paramRef := range pathItem.Put.Parameters {
				if paramRef.Value != nil && paramRef.Value.In == "body" && paramRef.Value.Schema != nil {
					schema = paramRef.Value.Schema.Value
					break
				}
			}
		}

		if schema == nil {
			continue
		}

		bestMatchSchema = schema
		if strings.HasSuffix(path, "}") {
			return bestMatchSchema, nil
		}
	}

	if bestMatchSchema != nil {
		return bestMatchSchema, nil
	}

	// Fallback: Try to find in definitions/schemas if the resourceType matches a schema name
	// This is less reliable as schema names are arbitrary, but sometimes they match.
	// For Azure, resourceType "Microsoft.ContainerService/managedClusters" might not match "ManagedCluster" directly without mapping.
	parts := strings.Split(searchType, "/")
	if len(parts) > 0 {
		name := parts[len(parts)-1]
		// Try exact match, case-insensitive match, and singularized match
		candidates := []string{name, strings.TrimSuffix(name, "s")}

		if doc.Components != nil && doc.Components.Schemas != nil {
			for schemaName, schemaRef := range doc.Components.Schemas {
				for _, candidate := range candidates {
					if strings.EqualFold(schemaName, candidate) {
						if schemaRef.Value != nil {
							return schemaRef.Value, nil
						}
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("resource type %s not found in spec", resourceType)
}

func azureARMInstanceResourceTypeFromPath(path string) (string, bool) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "", false
	}

	segments := strings.Split(trimmed, "/")
	providersIdx := -1
	for i, seg := range segments {
		if strings.EqualFold(seg, "providers") {
			providersIdx = i
			break
		}
	}
	if providersIdx == -1 || providersIdx+1 >= len(segments) {
		return "", false
	}

	provider := segments[providersIdx+1]
	if provider == "" {
		return "", false
	}

	typeSegments := make([]string, 0)
	for i := providersIdx + 2; i < len(segments); {
		seg := segments[i]
		if isPathParam(seg) {
			return "", false
		}
		if i+1 >= len(segments) || !isPathParam(segments[i+1]) {
			break
		}
		typeSegments = append(typeSegments, seg)
		i += 2
	}

	// Only treat this as an instance path if we consumed all segments and ended on a {name}.
	if providersIdx+2+2*len(typeSegments) != len(segments) {
		return "", false
	}
	if len(typeSegments) == 0 {
		return "", false
	}

	return provider + "/" + strings.Join(typeSegments, "/"), true
}

func isPathParam(segment string) bool {
	return strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}

// NavigateSchema traverses the schema properties based on the dot-separated path.
func NavigateSchema(schema *openapi3.Schema, path string) (*openapi3.Schema, error) {
	if path == "" {
		return schema, nil
	}
	parts := strings.Split(path, ".")
	current := schema
	for _, part := range parts {
		if current.Properties == nil {
			return nil, fmt.Errorf("path segment %s not found: schema has no properties", part)
		}
		prop, ok := current.Properties[part]
		if !ok {
			return nil, fmt.Errorf("property %s not found", part)
		}
		if prop.Value == nil {
			return nil, fmt.Errorf("property %s has nil schema", part)
		}
		if prop.Value.ReadOnly {
			return nil, nil // Indicate read-only property
		}
		current = prop.Value
	}
	return current, nil
}
