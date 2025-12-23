package terraform

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/matt-FFFFFF/tfmodmake/internal/hclgen"
	"github.com/zclconf/go-cty/cty"
)

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

		if len(propSchema.Enum) > 0 {
			var enumValuesRaw []string
			var enumTokens []hclwrite.Tokens
			for _, v := range propSchema.Enum {
				enumValuesRaw = append(enumValuesRaw, fmt.Sprintf("%v", v))
				enumTokens = append(enumTokens, hclwrite.TokensForValue(cty.StringVal(fmt.Sprintf("%v", v))))
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
			validationBody.SetAttributeValue("error_message", cty.StringVal(fmt.Sprintf("%s must be one of: %s.", tfName, strings.Join(enumValuesRaw, ", "))))
		}

		// Generate validation for numeric constraints
		if propSchema.Type != nil && (slices.Contains(*propSchema.Type, "integer") || slices.Contains(*propSchema.Type, "number")) {
			varRef := hclgen.TokensForTraversal("var", tfName)

			// Collect all numeric constraints
			var constraints []string
			var conditionParts []hclwrite.Tokens

			// Handle minimum constraint (with optional exclusiveMinimum modifier)
			if propSchema.Min != nil {
				minVal := *propSchema.Min
				isExclusive := propSchema.ExclusiveMin

				if isExclusive {
					constraints = append(constraints, fmt.Sprintf("> %v", minVal))
					var minCheck hclwrite.Tokens
					minCheck = append(minCheck, varRef...)
					minCheck = append(minCheck, &hclwrite.Token{Type: hclsyntax.TokenGreaterThan, Bytes: []byte(" > ")})
					minCheck = append(minCheck, hclwrite.TokensForValue(cty.NumberFloatVal(minVal))...)
					conditionParts = append(conditionParts, minCheck)
				} else {
					constraints = append(constraints, fmt.Sprintf(">= %v", minVal))
					var minCheck hclwrite.Tokens
					minCheck = append(minCheck, varRef...)
					minCheck = append(minCheck, &hclwrite.Token{Type: hclsyntax.TokenGreaterThanEq, Bytes: []byte(" >= ")})
					minCheck = append(minCheck, hclwrite.TokensForValue(cty.NumberFloatVal(minVal))...)
					conditionParts = append(conditionParts, minCheck)
				}
			}

			// Handle maximum constraint (with optional exclusiveMaximum modifier)
			if propSchema.Max != nil {
				maxVal := *propSchema.Max
				isExclusive := propSchema.ExclusiveMax

				if isExclusive {
					constraints = append(constraints, fmt.Sprintf("< %v", maxVal))
					var maxCheck hclwrite.Tokens
					maxCheck = append(maxCheck, varRef...)
					maxCheck = append(maxCheck, &hclwrite.Token{Type: hclsyntax.TokenLessThan, Bytes: []byte(" < ")})
					maxCheck = append(maxCheck, hclwrite.TokensForValue(cty.NumberFloatVal(maxVal))...)
					conditionParts = append(conditionParts, maxCheck)
				} else {
					constraints = append(constraints, fmt.Sprintf("<= %v", maxVal))
					var maxCheck hclwrite.Tokens
					maxCheck = append(maxCheck, varRef...)
					maxCheck = append(maxCheck, &hclwrite.Token{Type: hclsyntax.TokenLessThanEq, Bytes: []byte(" <= ")})
					maxCheck = append(maxCheck, hclwrite.TokensForValue(cty.NumberFloatVal(maxVal))...)
					conditionParts = append(conditionParts, maxCheck)
				}
			}

			// Handle multipleOf constraint
			if propSchema.MultipleOf != nil {
				multipleOfVal := *propSchema.MultipleOf
				constraints = append(constraints, fmt.Sprintf("multiple of %v", multipleOfVal))

				// Build: var.x % multipleOf == 0
				var modCheck hclwrite.Tokens
				modCheck = append(modCheck, varRef...)
				modCheck = append(modCheck, &hclwrite.Token{Type: hclsyntax.TokenPercent, Bytes: []byte(" % ")})
				modCheck = append(modCheck, hclwrite.TokensForValue(cty.NumberFloatVal(multipleOfVal))...)
				modCheck = append(modCheck, &hclwrite.Token{Type: hclsyntax.TokenEqualOp, Bytes: []byte(" == ")})
				modCheck = append(modCheck, hclwrite.TokensForValue(cty.NumberIntVal(0))...)
				conditionParts = append(conditionParts, modCheck)
			}

			// Only generate validation if there are constraints
			if len(conditionParts) > 0 {
				// Combine all constraint checks with &&
				var innerCondition hclwrite.Tokens
				for i, part := range conditionParts {
					if i > 0 {
						innerCondition = append(innerCondition, &hclwrite.Token{Type: hclsyntax.TokenAnd, Bytes: []byte(" &&\n      ")})
					}
					innerCondition = append(innerCondition, part...)
				}

				// Wrap in parentheses for multi-line formatting
				var wrappedInner hclwrite.Tokens
				wrappedInner = append(wrappedInner, &hclwrite.Token{Type: hclsyntax.TokenOParen, Bytes: []byte("(")})
				wrappedInner = append(wrappedInner, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n      ")})
				wrappedInner = append(wrappedInner, innerCondition...)
				wrappedInner = append(wrappedInner, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n    ")})
				wrappedInner = append(wrappedInner, &hclwrite.Token{Type: hclsyntax.TokenCParen, Bytes: []byte(")")})

				// Build final condition with null-safe short-circuit if not required
				var finalCondition hclwrite.Tokens
				if !isRequired {
					// Build: var.x == null || (...)
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenOParen, Bytes: []byte("(")})
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n    ")})
					finalCondition = append(finalCondition, varRef...)
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenEqualOp, Bytes: []byte(" == ")})
					finalCondition = append(finalCondition, hclwrite.TokensForIdentifier("null")...)
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenOr, Bytes: []byte(" ||")})
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n    ")})
					finalCondition = append(finalCondition, wrappedInner...)
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n  ")})
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenCParen, Bytes: []byte(")")})
				} else {
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenOParen, Bytes: []byte("(")})
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n    ")})
					finalCondition = append(finalCondition, innerCondition...)
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n  ")})
					finalCondition = append(finalCondition, &hclwrite.Token{Type: hclsyntax.TokenCParen, Bytes: []byte(")")})
				}

				// Build error message
				errorMsg := fmt.Sprintf("%s must be %s.", tfName, strings.Join(constraints, " and "))

				validation := varBody.AppendNewBlock("validation", nil)
				validationBody := validation.Body()
				validationBody.SetAttributeRaw("condition", finalCondition)
				validationBody.SetAttributeValue("error_message", cty.StringVal(errorMsg))
			}
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
