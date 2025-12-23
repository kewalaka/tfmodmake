package terraform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/matt-FFFFFF/tfmodmake/internal/openapi"
)

func TestSupportsLocation_ManagedIdentityUserAssigned(t *testing.T) {
	t.Parallel()

	specURL := "https://raw.githubusercontent.com/Azure/azure-rest-api-specs/62f4b6969f4273d444daec4a1d2bf9769820fca2/specification/msi/resource-manager/Microsoft.ManagedIdentity/ManagedIdentity/preview/2025-01-31-preview/ManagedIdentity.json"

	doc, err := openapi.LoadSpec(specURL)
	require.NoError(t, err)

	schema, err := openapi.FindResource(doc, "Microsoft.ManagedIdentity/userAssignedIdentities")
	require.NoError(t, err)

	assert.True(t, SupportsLocation(schema), "userAssignedIdentities should support location via TrackedResource inheritance")
}
