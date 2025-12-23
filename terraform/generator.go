//go:build tfmodmake_legacy_generator
// +build tfmodmake_legacy_generator

// Package terraform provides functions to generate Terraform variable and local definitions from OpenAPI schemas.
package terraform

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/matt-FFFFFF/tfmodmake/internal/hclgen"
	"github.com/zclconf/go-cty/cty"
)

// extractEnumValues extracts enum values from a schema, handling:
// - Direct enum arrays
// - x-ms-enum extensions
// - Schemas referenced via $ref (already resolved by kin-openapi)
// - Schemas composed via allOf
//
// Returns a slice of string values in stable order (sorted).
func extractEnumValues(schema *openapi3.Schema) []string {
	if schema == nil {
		return nil
	}

	var enumValues []string
	seen := make(map[string]struct{})

	// Helper to add unique enum values
	addEnum := func(val string) {
		if _, exists := seen[val]; !exists {
			enumValues = append(enumValues, val)
			seen[val] = struct{}{}
		}
	}

	// 1. Check for direct enum array
	if len(schema.Enum) > 0 {
		for _, v := range schema.Enum {
			addEnum(fmt.Sprintf("%v", v))
		}
	}

	// 2. Check for x-ms-enum extension
	// x-ms-enum can provide additional metadata like descriptions
	// Format: { "name": "EnumName", "modelAsString": true, "values": [{"value": "A"}, {"value": "B"}] }
	if schema.Extensions != nil {
		if raw, ok := schema.Extensions["x-ms-enum"]; ok {
			var xmsEnum map[string]any
			switch v := raw.(type) {
			case json.RawMessage:
				if err := json.Unmarshal(v, &xmsEnum); err == nil {
					if values, ok := xmsEnum["values"].([]any); ok {
						for _, item := range values {
							if itemMap, ok := item.(map[string]any); ok {
								if val, ok := itemMap["value"].(string); ok {
									addEnum(val)
								}
							}
						}
					}
				}
			case map[string]any:
				if values, ok := v["values"].([]any); ok {
					for _, item := range values {
						if itemMap, ok := item.(map[string]any); ok {
							if val, ok := itemMap["value"].(string); ok {
								addEnum(val)
							}
						}
					}
				}
			}
		}
	}

	// 3. Check allOf schemas (schema composition)
	for _, allOfRef := range schema.AllOf {
		if allOfRef != nil && allOfRef.Value != nil {
			childEnums := extractEnumValues(allOfRef.Value)
			for _, val := range childEnums {
				addEnum(val)
			}
		}
	}

	// Sort for stable output
	sort.Strings(enumValues)
	return enumValues
}

// secretField represents a secret field detected in the schema.
type secretField struct {
	// path is the JSON path to the field, e.g., "properties.daprAIInstrumentationKey"
	path string
	// varName is the snake_case variable name, e.g., "dapr_ai_instrumentation_key"
	varName string
	// schema is the OpenAPI schema for this field
	schema *openapi3.Schema
}

func isWritableProperty(schema *openapi3.Schema) bool {
	if schema == nil {
		return false
	}
	if schema.ReadOnly {
		return false
	}

	// Azure specs often annotate mutability using x-ms-mutability.
	// If it's present and does not include create/update, treat it as non-writable.
	if schema.Extensions != nil {
		if raw, ok := schema.Extensions["x-ms-mutability"]; ok {
			mutabilities := make([]string, 0)
			switch v := raw.(type) {
			case json.RawMessage:
				var decoded []string
				if err := json.Unmarshal(v, &decoded); err == nil {
					for _, item := range decoded {
						item = strings.ToLower(strings.TrimSpace(item))
						if item != "" {
							mutabilities = append(mutabilities, item)
						}
					}
				}
			case []string:
				for _, item := range v {
					item = strings.ToLower(strings.TrimSpace(item))
					if item != "" {
						mutabilities = append(mutabilities, item)
					}
				}
			case []any:
				for _, item := range v {
					if s, ok := item.(string); ok {
						mutabilities = append(mutabilities, strings.ToLower(strings.TrimSpace(s)))
					}
				}
			}

			if len(mutabilities) > 0 {
				for _, m := range mutabilities {
					if m == "create" || m == "update" {
						return true
					}
				}
				return false
			}
		}
	}

	return true
}

// isSecretField checks if a schema property has x-ms-secret: true extension.
func isSecretField(schema *openapi3.Schema) bool {
	if schema == nil || schema.Extensions == nil {
		return false
	}
	if val, ok := schema.Extensions["x-ms-secret"]; ok {
		if boolVal, ok := val.(bool); ok {
			return boolVal
		}
	}
	return false
}

// collectSecretFields traverses the schema and collects all fields marked with x-ms-secret.
func collectSecretFields(schema *openapi3.Schema, pathPrefix string) []secretField {
	var secrets []secretField
	if schema == nil {
		return secrets
	}

	var keys []string
	for k := range schema.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, name := range keys {
		prop := schema.Properties[name]
		if prop == nil || prop.Value == nil {
			continue
		}
		if !isWritableProperty(prop.Value) {
			continue
		}

		propSchema := prop.Value
		currentPath := name
		if pathPrefix != "" {
			currentPath = pathPrefix + "." + name
		}

		if isSecretField(propSchema) {
			secrets = append(secrets, secretField{
				path:    currentPath,
				varName: toSnakeCase(name),
				schema:  propSchema,
			})
		}

		// Recursively check nested objects
		if propSchema.Type != nil && slices.Contains(*propSchema.Type, "object") && len(propSchema.Properties) > 0 {
			nested := collectSecretFields(propSchema, currentPath)
			secrets = append(secrets, nested...)
		}

		// Check array items for nested secrets
		if propSchema.Type != nil && slices.Contains(*propSchema.Type, "array") {
			if propSchema.Items != nil && propSchema.Items.Value != nil {
				itemSchema := propSchema.Items.Value
				if itemSchema.Type != nil && slices.Contains(*itemSchema.Type, "object") && len(itemSchema.Properties) > 0 {
					nested := collectSecretFields(itemSchema, currentPath+"[]")
					secrets = append(secrets, nested...)
				}
			}
		}
	}

	return secrets
}

// Generate generates variables.tf, locals.tf, main.tf, and outputs.tf based on the schema.
func Generate(schema *openapi3.Schema, resourceType string, localName string, apiVersion string, supportsTags bool, supportsLocation bool) error {
	hasSchema := schema != nil

	// Collect secret fields from schema
	var secrets []secretField
	if hasSchema {
		secrets = collectSecretFields(schema, "")
	}

	if err := generateTerraform(); err != nil {
		return err
	}
	if err := generateVariables(schema, supportsTags, supportsLocation, secrets); err != nil {
		return err
	}
	if hasSchema {
		if err := generateLocals(schema, localName, secrets); err != nil {
			return err
		}
	}
	if err := generateMain(schema, resourceType, apiVersion, localName, supportsTags, supportsLocation, hasSchema, secrets); err != nil {
		return err
	}
	if err := generateOutputs(); err != nil {
		return err
	}
	return nil
}

func generateTerraform() error {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	tfBlock := body.AppendNewBlock("terraform", nil)
	tfBody := tfBlock.Body()
	tfBody.SetAttributeValue("required_version", cty.StringVal("~> 1.12"))

	providers := tfBody.AppendNewBlock("required_providers", nil)
	providers.Body().SetAttributeValue("azapi", cty.ObjectVal(map[string]cty.Value{
		"source":  cty.StringVal("azure/azapi"),
		"version": cty.StringVal("~> 2.7"),
	}))

	return hclgen.WriteFile("terraform.tf", file)
}

func generateVariables(schema *openapi3.Schema, supportsTags, supportsLocation bool, secrets []secretField) error {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	// Build a set of secret field variable names for quick lookup.
	secretVarNames := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		secretVarNames[secret.varName] = struct{}{}
	}

	appendVariable := func(name, description string, typeTokens hclwrite.Tokens) *hclwrite.Body {
		block := body.AppendNewBlock("variable", []string{name})
		varBody := block.Body()
		hclgen.SetDescriptionAttribute(varBody, strings.TrimSpace(description))
		varBody.SetAttributeRaw("type", typeTokens)
		return varBody
	}

	appendSchemaVariable := func(tfName, originalName string, propSchema *openapi3.Schema, required []string) (*hclwrite.Body, error) {
		if propSchema == nil {
			return nil, nil
		}

		tfType := mapType(propSchema)

		var nestedDocSchema *openapi3.Schema
		if propSchema.Type != nil && slices.Contains(*propSchema.Type, "object") {
			switch {
			case len(propSchema.Properties) > 0:
				nestedDocSchema = propSchema
			case propSchema.AdditionalProperties.Schema != nil && propSchema.AdditionalProperties.Schema.Value != nil:
				apSchema := propSchema.AdditionalProperties.Schema.Value
				if apSchema.Type != nil && slices.Contains(*apSchema.Type, "object") && len(apSchema.Properties) > 0 {
					nestedDocSchema = apSchema
				}
			}
		}
		isNestedObject := nestedDocSchema != nil

		varBody := appendVariable(tfName, "", tfType)

		if isNestedObject {
			var sb strings.Builder
			desc := propSchema.Description
			if desc == "" {
				if originalName != "" {
					desc = fmt.Sprintf("The %s of the resource.", originalName)
				} else {
					desc = fmt.Sprintf("The %s of the resource.", tfName)
				}
			}
			sb.WriteString(desc)
			sb.WriteString("\n\n")

			if nestedDocSchema != propSchema {
				sb.WriteString("Map values:\n")
			}

			sb.WriteString(buildNestedDescription(nestedDocSchema, ""))
			hclgen.SetDescriptionAttribute(varBody, sb.String())
		} else {
			description := propSchema.Description
			if description == "" {
				if originalName != "" {
					description = fmt.Sprintf("The %s of the resource.", originalName)
				} else {
					description = fmt.Sprintf("The %s of the resource.", tfName)
				}
			}
			hclgen.SetDescriptionAttribute(varBody, description)
		}

		isRequired := slices.Contains(required, originalName)
		if !isRequired {
			varBody.SetAttributeRaw("default", hclwrite.TokensForIdentifier("null"))
		}

		// Mark secret fields as ephemeral
		if _, ok := secretVarNames[tfName]; ok {
			varBody.SetAttributeValue("ephemeral", cty.True)
		}

		// Generate enum validation using the new helper function
		enumValues := extractEnumValues(propSchema)
		if len(enumValues) > 0 {
			var enumTokens []hclwrite.Tokens
			for _, val := range enumValues {
				enumTokens = append(enumTokens, hclwrite.TokensForValue(cty.StringVal(val)))
			}

			varRef := hclgen.TokensForTraversal("var", tfName)
			enumList := hclwrite.TokensForTuple(enumTokens)
			containsCall := hclwrite.TokensForFunctionCall("contains", enumList, varRef)

			var condition hclwrite.Tokens
			if !isRequired {
				condition = append(condition, varRef...)
				condition = append(condition, &hclwrite.Token{Type: hclsyntax.TokenEqualOp, Bytes: []byte(" == ")})
				condition = append(condition, hclwrite.TokensForIdentifier("null")...)
				condition = append(condition, &hclwrite.Token{Type: hclsyntax.TokenOr, Bytes: []byte(" || ")})
				condition = append(condition, containsCall...)
			} else {
				condition = containsCall
			}

			validation := varBody.AppendNewBlock("validation", nil)
			validationBody := validation.Body()
			validationBody.SetAttributeRaw("condition", condition)
			validationBody.SetAttributeValue("error_message", cty.StringVal(fmt.Sprintf("%s must be one of: %s.", tfName, strings.Join(enumValues, ", "))))
		}

		return varBody, nil
	}

	appendVariable("name", "The name of the resource.", hclwrite.TokensForIdentifier("string"))
	body.AppendNewline()

	appendVariable("parent_id", "The parent resource ID for this resource.", hclwrite.TokensForIdentifier("string"))
	body.AppendNewline()

	if supportsLocation {
		locationDescription := "The location of the resource."
		locationType := hclwrite.TokensForIdentifier("string")
		appendVariable("location", locationDescription, locationType)
		body.AppendNewline()
	}

	if supportsTags {
		tagsBody := appendVariable("tags", "Tags to apply to the resource.", hclwrite.TokensForFunctionCall("map", hclwrite.TokensForIdentifier("string")))
		tagsBody.SetAttributeValue("default", cty.NullVal(cty.Map(cty.String)))
		body.AppendNewline()
	}

	seenNames := map[string]struct{}{
		"name":      {},
		"parent_id": {},
	}
	if supportsLocation {
		seenNames["location"] = struct{}{}
	}
	if supportsTags {
		seenNames["tags"] = struct{}{}
	}

	var keys []string
	if schema != nil {
		for k := range schema.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}

	for i, name := range keys {
		prop := schema.Properties[name]
		if prop == nil || prop.Value == nil {
			continue
		}
		if supportsTags && name == "tags" {
			continue
		}
		if supportsLocation && name == "location" {
			continue
		}
		propSchema := prop.Value

		if !isWritableProperty(propSchema) {
			continue
		}

		// Flatten the top-level "properties" bag into individual variables.
		if name == "properties" {
			if propSchema.Type != nil && slices.Contains(*propSchema.Type, "object") && len(propSchema.Properties) > 0 {
				var childKeys []string
				for ck := range propSchema.Properties {
					childKeys = append(childKeys, ck)
				}
				sort.Strings(childKeys)

				for _, ck := range childKeys {
					childRef := propSchema.Properties[ck]
					if childRef == nil || childRef.Value == nil {
						continue
					}
					childSchema := childRef.Value
					if !isWritableProperty(childSchema) {
						continue
					}
					tfName := toSnakeCase(ck)
					if tfName == "" {
						return fmt.Errorf("could not derive terraform variable name for properties.%s", ck)
					}
					if _, exists := seenNames[tfName]; exists {
						return fmt.Errorf("terraform variable name collision: %q (from properties.%s)", tfName, ck)
					}
					seenNames[tfName] = struct{}{}

					if _, err := appendSchemaVariable(tfName, ck, childSchema, propSchema.Required); err != nil {
						return err
					}
					body.AppendNewline()
				}
				continue
			}
			// If "properties" isn't a concrete object, fall back to the old behavior.
		}

		tfName := toSnakeCase(name)
		if tfName == "" {
			return fmt.Errorf("could not derive terraform variable name for %s", name)
		}
		if _, exists := seenNames[tfName]; exists {
			return fmt.Errorf("terraform variable name collision: %q (from %s)", tfName, name)
		}
		seenNames[tfName] = struct{}{}
		if _, err := appendSchemaVariable(tfName, name, propSchema, schema.Required); err != nil {
			return err
		}

		if i < len(keys)-1 {
			body.AppendNewline()
		}
	}

	// Add secret field variables (extracted from nested structures)
	secretBlockAdded := false
	for _, secret := range secrets {
		// If the variable already exists (e.g., flattened root properties), don't redeclare it.
		// The existing variable will already be marked ephemeral via secretVarNames.
		if _, exists := seenNames[secret.varName]; exists {
			continue
		}
		if !secretBlockAdded && len(keys) > 0 {
			body.AppendNewline()
			secretBlockAdded = true
		}

		secretVarBody := appendVariable(
			secret.varName,
			secret.schema.Description,
			mapType(secret.schema),
		)

		seenNames[secret.varName] = struct{}{}
		secretVarBody.SetAttributeRaw("default", hclwrite.TokensForIdentifier("null"))
		secretVarBody.SetAttributeValue("ephemeral", cty.True)

		body.AppendNewline()
	}

	// Add secret version variables
	for i, secret := range secrets {
		if i == 0 && len(keys) > 0 {
			body.AppendNewline()
		}
		versionVarName := secret.varName + "_version"
		if _, exists := seenNames[versionVarName]; exists {
			return fmt.Errorf("terraform variable name collision: %q (from secret version var)", versionVarName)
		}
		versionBody := appendVariable(
			versionVarName,
			fmt.Sprintf("Version tracker for %s. Must be set when %s is provided.", secret.varName, secret.varName),
			hclwrite.TokensForIdentifier("number"),
		)
		seenNames[versionVarName] = struct{}{}

		versionBody.SetAttributeRaw("default", hclwrite.TokensForIdentifier("null"))

		// Add validation that version must be set when secret is set
		validation := versionBody.AppendNewBlock("validation", nil)
		validationBody := validation.Body()

		// Build condition: var.secret == null || var.secret_version != null
		secretVarRef := hclgen.TokensForTraversal("var", secret.varName)
		versionVarRef := hclgen.TokensForTraversal("var", versionVarName)

		var condition hclwrite.Tokens
		condition = append(condition, secretVarRef...)
		condition = append(condition, &hclwrite.Token{Type: hclsyntax.TokenEqualOp, Bytes: []byte(" == ")})
		condition = append(condition, hclwrite.TokensForIdentifier("null")...)
		condition = append(condition, &hclwrite.Token{Type: hclsyntax.TokenOr, Bytes: []byte(" || ")})
		condition = append(condition, versionVarRef...)
		condition = append(condition, &hclwrite.Token{Type: hclsyntax.TokenNotEqual, Bytes: []byte(" != ")})
		condition = append(condition, hclwrite.TokensForIdentifier("null")...)

		validationBody.SetAttributeRaw("condition", condition)
		validationBody.SetAttributeValue(
			"error_message",
			cty.StringVal(fmt.Sprintf("When %s is set, %s must also be set.", secret.varName, versionVarName)),
		)

		if i < len(secrets)-1 {
			body.AppendNewline()
		}
	}

	return hclgen.WriteFile("variables.tf", file)
}

func generateLocals(schema *openapi3.Schema, localName string, secrets []secretField) error {
	if schema == nil {
		return nil
	}

	file := hclwrite.NewEmptyFile()
	body := file.Body()

	locals := body.AppendNewBlock("locals", nil)
	localBody := locals.Body()

	secretPaths := newSecretPathSet(secrets)
	valueExpression := constructValue(schema, hclwrite.TokensForIdentifier("var"), true, secretPaths, "")
	localBody.SetAttributeRaw(localName, valueExpression)

	return hclgen.WriteFile("locals.tf", file)
}

func newSecretPathSet(secrets []secretField) map[string]struct{} {
	if len(secrets) == 0 {
		return nil
	}
	paths := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		p := strings.TrimSpace(secret.path)
		if p == "" {
			continue
		}
		paths[p] = struct{}{}
	}
	return paths
}

func isHCLIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func tokensForObjectKey(key string) hclwrite.Tokens {
	if isHCLIdentifier(key) {
		return hclwrite.TokensForIdentifier(key)
	}
	return hclwrite.TokensForValue(cty.StringVal(key))
}

func constructFlattenedRootPropertiesValue(schema *openapi3.Schema, accessPath hclwrite.Tokens, secretPaths map[string]struct{}) hclwrite.Tokens {
	// schema represents the OpenAPI schema at root.properties.
	// The Terraform variables are flattened to var.<child> rather than var.properties.<child>.

	if schema == nil {
		return hclwrite.TokensForIdentifier("null")
	}

	var attrs []hclwrite.ObjectAttrTokens
	var keys []string
	for k := range schema.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Keep object construction simple; AzAPI can ignore null properties when
	// ignore_null_property is enabled on the resource.
	for _, k := range keys {
		prop := schema.Properties[k]
		if prop == nil || prop.Value == nil {
			continue
		}
		if !isWritableProperty(prop.Value) {
			continue
		}

		if secretPaths != nil {
			if _, ok := secretPaths["properties."+k]; ok {
				continue
			}
		}

		snakeName := toSnakeCase(k)
		var childAccess hclwrite.Tokens
		childAccess = append(childAccess, accessPath...)
		childAccess = append(childAccess, &hclwrite.Token{Type: hclsyntax.TokenDot, Bytes: []byte(".")})
		childAccess = append(childAccess, hclwrite.TokensForIdentifier(snakeName)...)

		childValue := constructValue(prop.Value, childAccess, false, secretPaths, "properties."+k)
		attrs = append(attrs, hclwrite.ObjectAttrTokens{
			Name:  tokensForObjectKey(k),
			Value: childValue,
		})
	}

	return hclwrite.TokensForObject(attrs)
}

func constructValue(schema *openapi3.Schema, accessPath hclwrite.Tokens, isRoot bool, secretPaths map[string]struct{}, pathPrefix string) hclwrite.Tokens {
	if schema.Type == nil {
		return accessPath
	}

	types := *schema.Type

	if slices.Contains(types, "object") {
		if len(schema.Properties) == 0 {
			if schema.AdditionalProperties.Schema != nil && schema.AdditionalProperties.Schema.Value != nil {
				mappedValue := constructValue(schema.AdditionalProperties.Schema.Value, hclwrite.TokensForIdentifier("value"), false, secretPaths, pathPrefix)

				var tokens hclwrite.Tokens
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenOBrace, Bytes: []byte("{")})
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("for")})
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("k")})
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(",")})
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("value")})
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("in")})
				tokens = append(tokens, accessPath...)
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenColon, Bytes: []byte(":")})
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("k")})
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenFatArrow, Bytes: []byte("=>")})
				tokens = append(tokens, mappedValue...)
				tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrace, Bytes: []byte("}")})

				if !isRoot {
					return hclgen.NullEqualityTernary(accessPath, tokens)
				}
				return tokens
			}
			return accessPath // map(string) or free-form, passed as is
		}

		var attrs []hclwrite.ObjectAttrTokens
		var keys []string
		for k := range schema.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			prop := schema.Properties[k]
			if prop == nil || prop.Value == nil {
				continue
			}

			if !isWritableProperty(prop.Value) {
				continue
			}

			childPath := k
			if pathPrefix != "" {
				childPath = pathPrefix + "." + k
			}
			if secretPaths != nil {
				if _, ok := secretPaths[childPath]; ok {
					continue
				}
			}

			// Flatten the top-level "properties" bag into separate variables.
			if isRoot && k == "properties" && prop.Value.Type != nil && slices.Contains(*prop.Value.Type, "object") && len(prop.Value.Properties) > 0 {
				childValue := constructFlattenedRootPropertiesValue(prop.Value, accessPath, secretPaths)
				attrs = append(attrs, hclwrite.ObjectAttrTokens{
					Name:  tokensForObjectKey(k),
					Value: childValue,
				})
				continue
			}

			snakeName := toSnakeCase(k)
			var childAccess hclwrite.Tokens
			childAccess = append(childAccess, accessPath...)
			childAccess = append(childAccess, &hclwrite.Token{Type: hclsyntax.TokenDot, Bytes: []byte(".")})
			childAccess = append(childAccess, hclwrite.TokensForIdentifier(snakeName)...)

			childValue := constructValue(prop.Value, childAccess, false, secretPaths, childPath)
			attrs = append(attrs, hclwrite.ObjectAttrTokens{
				Name:  tokensForObjectKey(k),
				Value: childValue,
			})
		}

		objTokens := hclwrite.TokensForObject(attrs)
		if !isRoot {
			return hclgen.NullEqualityTernary(accessPath, objTokens)
		}
		return objTokens
	}

	if slices.Contains(types, "array") {
		if schema.Items != nil && schema.Items.Value != nil {
			childValue := constructValue(schema.Items.Value, hclwrite.TokensForIdentifier("item"), false, secretPaths, pathPrefix+"[]")

			var tokens hclwrite.Tokens
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")})
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("for")})
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("item")})
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("in")})
			tokens = append(tokens, accessPath...)
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenColon, Bytes: []byte(":")})
			tokens = append(tokens, childValue...)
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})

			if !isRoot {
				return hclgen.NullEqualityTernary(accessPath, tokens)
			}
			return tokens
		}
		return accessPath
	}

	return accessPath
}

func mapType(schema *openapi3.Schema) hclwrite.Tokens {
	if schema.Type == nil {
		return hclwrite.TokensForIdentifier("any")
	}

	types := *schema.Type

	if slices.Contains(types, "string") {
		return hclwrite.TokensForIdentifier("string")
	}
	if slices.Contains(types, "integer") || slices.Contains(types, "number") {
		return hclwrite.TokensForIdentifier("number")
	}
	if slices.Contains(types, "boolean") {
		return hclwrite.TokensForIdentifier("bool")
	}
	if slices.Contains(types, "array") {
		elemType := hclwrite.TokensForIdentifier("any")
		if schema.Items != nil && schema.Items.Value != nil {
			elemType = mapType(schema.Items.Value)
		}
		return hclwrite.TokensForFunctionCall("list", elemType)
	}
	if slices.Contains(types, "object") {
		if len(schema.Properties) == 0 {
			if schema.AdditionalProperties.Schema != nil && schema.AdditionalProperties.Schema.Value != nil {
				valueType := mapType(schema.AdditionalProperties.Schema.Value)
				return hclwrite.TokensForFunctionCall("map", valueType)
			}
			return hclwrite.TokensForFunctionCall("map", hclwrite.TokensForIdentifier("string"))
		}
		var attrs []hclwrite.ObjectAttrTokens

		// Sort properties
		var keys []string
		for k := range schema.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			prop := schema.Properties[k]
			if prop == nil || prop.Value == nil {
				continue
			}
			if !isWritableProperty(prop.Value) {
				continue
			}
			fieldType := mapType(prop.Value)

			// Check if optional
			isOptional := true
			if slices.Contains(schema.Required, k) {
				isOptional = false
			}

			if isOptional {
				fieldType = hclwrite.TokensForFunctionCall("optional", fieldType)
			}
			attrs = append(attrs, hclwrite.ObjectAttrTokens{
				Name:  hclwrite.TokensForIdentifier(toSnakeCase(k)),
				Value: fieldType,
			})
		}
		return hclwrite.TokensForFunctionCall("object", hclwrite.TokensForObject(attrs))
	}

	return hclwrite.TokensForIdentifier("any")
}

func buildNestedDescription(schema *openapi3.Schema, indent string) string {
	var sb strings.Builder

	type keyPair struct {
		original string
		snake    string
	}
	var childKeys []keyPair
	for k := range schema.Properties {
		childKeys = append(childKeys, keyPair{original: k, snake: toSnakeCase(k)})
	}
	sort.Slice(childKeys, func(i, j int) bool {
		return childKeys[i].snake < childKeys[j].snake
	})

	for _, pair := range childKeys {
		k := pair.original
		childProp := schema.Properties[k]
		if childProp == nil || childProp.Value == nil {
			continue
		}
		val := childProp.Value

		if !isWritableProperty(val) {
			continue
		}

		childDesc := val.Description
		if childDesc == "" {
			childDesc = fmt.Sprintf("The %s property.", k)
		}
		childDesc = strings.ReplaceAll(childDesc, "\n", " ")

		sb.WriteString(fmt.Sprintf("%s- `%s` - %s\n", indent, pair.snake, childDesc))

		isNested := false
		if val.Type != nil {
			if slices.Contains(*val.Type, "object") {
				isNested = true
			}
		}
		if isNested && len(val.Properties) > 0 {
			sb.WriteString(buildNestedDescription(val, indent+"  "))
		}
	}
	return sb.String()
}

func toSnakeCase(input string) string {
	var sb strings.Builder
	runes := []rune(input)

	prevWasUnderscore := false
	wroteAny := false

	isAlnum := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	prevAlnum := func(i int) (rune, bool) {
		for j := i - 1; j >= 0; j-- {
			if isAlnum(runes[j]) {
				return runes[j], true
			}
		}
		return 0, false
	}
	nextAlnum := func(i int) (rune, bool) {
		for j := i + 1; j < len(runes); j++ {
			if isAlnum(runes[j]) {
				return runes[j], true
			}
		}
		return 0, false
	}

	for i, r := range runes {
		// Treat non-alphanumerics (e.g. '-', '.', spaces) as separators.
		if !isAlnum(r) {
			if wroteAny && !prevWasUnderscore {
				sb.WriteRune('_')
				prevWasUnderscore = true
			}
			continue
		}

		if unicode.IsUpper(r) {
			if p, ok := prevAlnum(i); ok {
				if (unicode.IsLower(p) || unicode.IsDigit(p)) && !prevWasUnderscore {
					sb.WriteRune('_')
				}
				if unicode.IsUpper(p) {
					// Split acronyms when the next alnum is lower (HTTPClient -> http_client)
					if n, ok := nextAlnum(i); ok && unicode.IsLower(n) {
						// Look ahead for a lower-case sequence length
						j := i + 1
						for j < len(runes) {
							if !isAlnum(runes[j]) {
								j++
								continue
							}
							if !unicode.IsLower(runes[j]) {
								break
							}
							j++
						}
						lowerLen := j - (i + 1)

						if lowerLen > 1 && !prevWasUnderscore {
							sb.WriteRune('_')
						}
						if lowerLen == 1 && n != 's' && !prevWasUnderscore {
							sb.WriteRune('_')
						}
					}
				}
			}
		}

		sb.WriteRune(unicode.ToLower(r))
		wroteAny = true
		prevWasUnderscore = false
	}

	out := strings.Trim(sb.String(), "_")
	if out == "" {
		return out
	}
	if len(out) > 0 && out[0] >= '0' && out[0] <= '9' {
		out = "field_" + out
	}
	return out
}

// SupportsTags reports whether the schema includes a writable "tags" property, following allOf inheritance.
func SupportsTags(schema *openapi3.Schema) bool {
	return hasWritableProperty(schema, "tags")
}

// SupportsLocation reports whether the schema includes a writable "location" property, following allOf inheritance.
func SupportsLocation(schema *openapi3.Schema) bool {
	return hasWritableProperty(schema, "location")
}

func hasWritableProperty(schema *openapi3.Schema, path string) bool {
	if schema == nil || path == "" {
		return false
	}
	segments := strings.Split(path, ".")
	return hasWritablePropertyRecursive(schema, segments, make(map[*openapi3.Schema]struct{}))
}

func hasWritablePropertyRecursive(schema *openapi3.Schema, segments []string, visited map[*openapi3.Schema]struct{}) bool {
	if schema == nil || len(segments) == 0 {
		return false
	}
	if _, seen := visited[schema]; seen {
		return false
	}
	visited[schema] = struct{}{}

	propName := segments[0]
	propRef, ok := schema.Properties[propName]
	if ok && propRef != nil && propRef.Value != nil && isWritableProperty(propRef.Value) {
		if len(segments) == 1 {
			return true
		}
		if hasWritablePropertyRecursive(propRef.Value, segments[1:], visited) {
			return true
		}
	}

	for _, ref := range schema.AllOf {
		if ref == nil || ref.Value == nil {
			continue
		}
		if hasWritablePropertyRecursive(ref.Value, segments, visited) {
			return true
		}
	}

	return false
}

func cleanTypeString(typeStr string) string {
	segments := strings.Split(typeStr, "/")
	cleaned := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			continue
		}
		cleaned = append(cleaned, segment)
	}
	return strings.Join(cleaned, "/")
}

type sensitiveBodyNode struct {
	children map[string]*sensitiveBodyNode
	secret   *secretField
}

func (n *sensitiveBodyNode) ensureChild(key string) *sensitiveBodyNode {
	if n.children == nil {
		n.children = make(map[string]*sensitiveBodyNode)
	}
	child, ok := n.children[key]
	if !ok {
		child = &sensitiveBodyNode{}
		n.children[key] = child
	}
	return child
}

func tokensForSensitiveBody(secrets []secretField, valueFor func(secretField) hclwrite.Tokens) hclwrite.Tokens {
	root := &sensitiveBodyNode{}
	for i := range secrets {
		path := strings.TrimSpace(secrets[i].path)
		if path == "" {
			continue
		}
		segments := strings.Split(path, ".")
		node := root
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			node = node.ensureChild(seg)
		}
		node.secret = &secrets[i]
	}

	var render func(node *sensitiveBodyNode) hclwrite.Tokens
	render = func(node *sensitiveBodyNode) hclwrite.Tokens {
		if node == nil || len(node.children) == 0 {
			return hclwrite.TokensForObject(nil)
		}
		keys := make([]string, 0, len(node.children))
		for k := range node.children {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		attrs := make([]hclwrite.ObjectAttrTokens, 0, len(keys))
		for _, k := range keys {
			child := node.children[k]
			var value hclwrite.Tokens
			if child != nil && child.secret != nil && len(child.children) == 0 {
				value = valueFor(*child.secret)
			} else {
				value = render(child)
			}
			attrs = append(attrs, hclwrite.ObjectAttrTokens{
				Name:  tokensForObjectKey(k),
				Value: value,
			})
		}
		return hclwrite.TokensForObject(attrs)
	}

	return render(root)
}

func generateMain(schema *openapi3.Schema, resourceType, apiVersion, localName string, supportsTags, supportsLocation, hasSchema bool, secrets []secretField) error {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	apiVersion = strings.TrimSpace(apiVersion)
	if apiVersion == "" {
		apiVersion = "apiVersion"
	}
	resourceTypeWithAPIVersion := fmt.Sprintf("%s@%s", cleanTypeString(resourceType), apiVersion)

	resourceBlock := body.AppendNewBlock("resource", []string{"azapi_resource", "this"})
	resourceBody := resourceBlock.Body()
	resourceBody.SetAttributeValue("type", cty.StringVal(resourceTypeWithAPIVersion))
	resourceBody.SetAttributeRaw("name", hclgen.TokensForTraversal("var", "name"))
	resourceBody.SetAttributeRaw("parent_id", hclgen.TokensForTraversal("var", "parent_id"))
	resourceBody.SetAttributeValue("ignore_null_property", cty.BoolVal(true))

	if supportsLocation {
		resourceBody.SetAttributeRaw("location", hclgen.TokensForTraversal("var", "location"))
	}

	resourceBody.SetAttributeValue("body", cty.EmptyObjectVal)
	if hasSchema {
		// If the schema already has a top-level "properties" field, we are generating the full resource body.
		// Otherwise, we assume the schema itself represents the properties object (e.g. -root properties).
		_, hasTopLevelProperties := schema.Properties["properties"]
		if hasTopLevelProperties {
			resourceBody.SetAttributeRaw("body", hclgen.TokensForTraversal("local", localName))
		} else {
			resourceBody.SetAttributeRaw("body", hclwrite.TokensForObject(
				[]hclwrite.ObjectAttrTokens{
					{
						Name:  hclwrite.TokensForIdentifier("properties"),
						Value: hclgen.TokensForTraversal("local", localName),
					},
				},
			))
		}
	}

	// Add sensitive_body if there are secrets
	if len(secrets) > 0 {
		resourceBody.SetAttributeRaw("sensitive_body", tokensForSensitiveBody(secrets, func(secret secretField) hclwrite.Tokens {
			return hclgen.TokensForTraversal("var", secret.varName)
		}))

		// Add sensitive_body_version map
		var versionAttrs []hclwrite.ObjectAttrTokens
		for _, secret := range secrets {
			versionVarName := secret.varName + "_version"
			versionAttrs = append(versionAttrs, hclwrite.ObjectAttrTokens{
				Name:  hclwrite.TokensForValue(cty.StringVal(secret.path)),
				Value: hclgen.TokensForTraversal("var", versionVarName),
			})
		}
		resourceBody.SetAttributeRaw("sensitive_body_version", hclwrite.TokensForObject(versionAttrs))
	}

	if supportsTags {
		resourceBody.SetAttributeRaw("tags", hclgen.TokensForTraversal("var", "tags"))
	}

	resourceBody.SetAttributeValue("response_export_values", cty.ListValEmpty(cty.String))

	return hclgen.WriteFile("main.tf", file)
}

func generateOutputs() error {
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	resourceID := body.AppendNewBlock("output", []string{"resource_id"})
	resourceIDBody := resourceID.Body()
	resourceIDBody.SetAttributeValue("description", cty.StringVal("The ID of the created resource."))
	resourceIDBody.SetAttributeRaw("value", hclgen.TokensForTraversal("azapi_resource", "this", "id"))

	name := body.AppendNewBlock("output", []string{"name"})
	nameBody := name.Body()
	nameBody.SetAttributeValue("description", cty.StringVal("The name of the created resource."))
	nameBody.SetAttributeRaw("value", hclgen.TokensForTraversal("azapi_resource", "this", "name"))

	return hclgen.WriteFile("outputs.tf", file)
}
