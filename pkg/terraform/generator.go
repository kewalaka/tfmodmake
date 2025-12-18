// Package terraform provides functions to generate Terraform variable and local definitions from OpenAPI schemas.
package terraform

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
)

// Generate generates variables.tf, locals.tf, main.tf, and outputs.tf based on the schema.
func Generate(schema *openapi3.Schema, resourceType string, localName string, supportsTags bool) error {
	if err := generateVariables(schema, supportsTags); err != nil {
		return err
	}
	if err := generateLocals(schema, localName); err != nil {
		return err
	}
	if err := generateMain(resourceType, localName, supportsTags); err != nil {
		return err
	}
	if err := generateOutputs(); err != nil {
		return err
	}
	return nil
}

func generateVariables(schema *openapi3.Schema, supportsTags bool) error {
	f, err := os.Create("variables.tf")
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprint(f, `variable "name" {
  description = "The name of the resource."
  type        = string
}

variable "parent_id" {
  description = "The parent resource ID for this resource."
  type        = string
}

`)

	if supportsTags {
		fmt.Fprint(f, `
variable "tags" {
  description = "Tags to apply to the resource."
  type        = map(string)
  default     = {}
}
`)
	}

	fmt.Fprint(f, "\n")

	// Sort properties for deterministic output
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
		propSchema := prop.Value

		if propSchema.ReadOnly {
			continue
		}

		tfName := toSnakeCase(name)
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

		fmt.Fprintf(f, "variable \"%s\" {\n", tfName)

		if isNestedObject {
			var sb strings.Builder
			desc := propSchema.Description
			if desc == "" {
				desc = fmt.Sprintf("The %s of the resource.", name)
			}
			sb.WriteString(desc)
			sb.WriteString("\n\n")

			if nestedDocSchema != propSchema {
				sb.WriteString("Map values:\n")
			}

			sb.WriteString(buildNestedDescription(nestedDocSchema, ""))

			fmt.Fprintf(f, "  description = <<-DESCRIPTION\n%s  DESCRIPTION\n", sb.String())
		} else {
			description := propSchema.Description
			if description == "" {
				description = fmt.Sprintf("The %s of the resource.", name)
			}
			description = strings.ReplaceAll(description, "\"", "\\\"")
			description = strings.ReplaceAll(description, "\n", " ")
			fmt.Fprintf(f, "  description = \"%s\"\n", description)
		}

		fmt.Fprintf(f, "  type        = %s\n", tfType)

		isRequired := false
		if slices.Contains(schema.Required, name) {
			isRequired = true
		}

		if !isRequired {
			fmt.Fprintf(f, "  default     = null\n")
		}

		// Validation for enums
		if len(propSchema.Enum) > 0 {
			var enumValues []string
			var enumValuesRaw []string
			for _, v := range propSchema.Enum {
				enumValues = append(enumValues, fmt.Sprintf("\"%v\"", v))
				enumValuesRaw = append(enumValuesRaw, fmt.Sprintf("%v", v))
			}
			enumStr := fmt.Sprintf("[%s]", strings.Join(enumValues, ", "))

			fmt.Fprintf(f, "\n  validation {\n")
			if !isRequired {
				fmt.Fprintf(f, "    condition     = var.%s == null || contains(%s, var.%s)\n", tfName, enumStr, tfName)
			} else {
				fmt.Fprintf(f, "    condition     = contains(%s, var.%s)\n", enumStr, tfName)
			}
			fmt.Fprintf(f, "    error_message = \"%s must be one of: %s.\"\n", tfName, strings.Join(enumValuesRaw, ", "))
			fmt.Fprintf(f, "  }\n")
		}

		fmt.Fprintf(f, "}\n\n")
	}

	return nil
}

func generateLocals(schema *openapi3.Schema, localName string) error {
	f, err := os.Create("locals.tf")
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "locals {\n")

	// We construct the body recursively
	body := constructValue(schema, "var", true)

	// The body returned by constructValue for the root object will be something like:
	// {
	//   location = var.location
	//   ...
	// }
	// We want to assign it to the specified local name

	fmt.Fprintf(f, "  %s = %s\n", localName, body)
	fmt.Fprintf(f, "}\n")

	return nil
}

func constructValue(schema *openapi3.Schema, accessPath string, isRoot bool) string {
	if schema.Type == nil {
		return accessPath
	}

	types := *schema.Type

	if slices.Contains(types, "object") {
		if len(schema.Properties) == 0 {
			if schema.AdditionalProperties.Schema != nil && schema.AdditionalProperties.Schema.Value != nil {
				mappedValue := constructValue(schema.AdditionalProperties.Schema.Value, "value", false)
				return fmt.Sprintf("%s == null ? null : { for k, value in %s : k => %s }", accessPath, accessPath, mappedValue)
			}
			return accessPath // map(string) or free-form, passed as is
		}

		var fields []string
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

			if prop.Value.ReadOnly {
				continue
			}

			snakeName := toSnakeCase(k)
			childAccess := fmt.Sprintf("%s.%s", accessPath, snakeName)
			if isRoot {
				// For root variables, access is var.snake_name
				childAccess = fmt.Sprintf("var.%s", snakeName)
			}

			childValue := constructValue(prop.Value, childAccess, false)
			fields = append(fields, fmt.Sprintf("%s = %s", k, childValue))
		}

		objStr := fmt.Sprintf("{\n%s\n}", strings.Join(fields, "\n"))

		if !isRoot {
			// If not root, handle null check
			return fmt.Sprintf("%s == null ? null : %s", accessPath, objStr)
		}
		return objStr
	}

	if slices.Contains(types, "array") {
		if schema.Items != nil && schema.Items.Value != nil {
			// [for x in accessPath : constructValue(items, x)]
			// We need a unique iterator variable name if nested?
			// Simple "item" might conflict if nested arrays?
			// Let's use a simple heuristic or just "item" since HCL scoping handles it?
			// Actually HCL `for` expressions create a new scope.

			childValue := constructValue(schema.Items.Value, "item", false)
			return fmt.Sprintf("%s == null ? null : [for item in %s : %s]", accessPath, accessPath, childValue)
		}
		return accessPath
	}

	return accessPath
}

func mapType(schema *openapi3.Schema) string {
	if schema.Type == nil {
		return "any"
	}

	types := *schema.Type

	if slices.Contains(types, "string") {
		return "string"
	}
	if slices.Contains(types, "integer") || slices.Contains(types, "number") {
		return "number"
	}
	if slices.Contains(types, "boolean") {
		return "bool"
	}
	if slices.Contains(types, "array") {
		elemType := "any"
		if schema.Items != nil && schema.Items.Value != nil {
			elemType = mapType(schema.Items.Value)
		}
		return fmt.Sprintf("list(%s)", elemType)
	}
	if slices.Contains(types, "object") {
		if len(schema.Properties) == 0 {
			if schema.AdditionalProperties.Schema != nil && schema.AdditionalProperties.Schema.Value != nil {
				valueType := mapType(schema.AdditionalProperties.Schema.Value)
				return fmt.Sprintf("map(%s)", valueType)
			}
			return "map(string)" // Fallback for free-form objects
		}
		var fields []string

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
			if prop.Value.ReadOnly {
				continue
			}
			fieldType := mapType(prop.Value)

			// Check if optional
			isOptional := true
			if slices.Contains(schema.Required, k) {
				isOptional = false
			}

			if isOptional {
				fields = append(fields, fmt.Sprintf("%s = optional(%s)", toSnakeCase(k), fieldType))
			} else {
				fields = append(fields, fmt.Sprintf("%s = %s", toSnakeCase(k), fieldType))
			}
		}
		return fmt.Sprintf("object({\n    %s\n  })", strings.Join(fields, "\n    "))
	}

	return "any"
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

		if val.ReadOnly {
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
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				if unicode.IsLower(prev) || unicode.IsDigit(prev) {
					sb.WriteRune('_')
				} else if unicode.IsUpper(prev) {
					// Check if we should split here
					// Standard rule: split if next is lower
					if i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
						// Exception: if the lower part is just 's' (plural acronym), don't split.

						// Look ahead for lower case sequence
						j := i + 1
						for j < len(runes) && unicode.IsLower(runes[j]) {
							j++
						}
						lowerLen := j - (i + 1)

						if lowerLen > 1 {
							sb.WriteRune('_')
						} else if lowerLen == 1 && runes[i+1] != 's' {
							sb.WriteRune('_')
						}
					}
				}
			}
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return sb.String()
}

// SupportsTags reports whether the schema includes a writable top-level "tags" property.
func SupportsTags(schema *openapi3.Schema) bool {
	if schema == nil {
		return false
	}
	tagsProp, ok := schema.Properties["tags"]
	if !ok || tagsProp == nil || tagsProp.Value == nil {
		return false
	}
	if tagsProp.Value.ReadOnly {
		return false
	}
	return true
}

func generateMain(resourceType, localName string, supportsTags bool) error {
	f, err := os.Create("main.tf")
	if err != nil {
		return err
	}
	defer f.Close()

	resourceTypeWithAPIVersion := fmt.Sprintf("%s@apiVersion", resourceType)

	fmt.Fprintf(f, "resource \"azapi_resource\" \"this\" {\n")
	fmt.Fprintf(f, "  type      = \"%s\"\n", resourceTypeWithAPIVersion)
	fmt.Fprintf(f, "  name      = var.name\n")
	fmt.Fprintf(f, "  parent_id = var.parent_id\n")
	fmt.Fprintf(f, "  body = {\n")
	fmt.Fprintf(f, "    properties = local.%s\n", localName)
	fmt.Fprintf(f, "  }\n")
	if supportsTags {
		fmt.Fprintf(f, "  tags = var.tags\n")
	}
	fmt.Fprintf(f, "}\n")

	return nil
}

func generateOutputs() error {
	f, err := os.Create("outputs.tf")
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprint(f, `output "resource_id" {
  description = "The ID of the created resource."
  value       = azapi_resource.this.id
}

output "name" {
  description = "The name of the created resource."
  value       = azapi_resource.this.name
}
`)

	return nil
}
