package terraform

import (
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/matt-FFFFFF/tfmodmake/internal/hclgen"
	"github.com/zclconf/go-cty/cty"
)

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
