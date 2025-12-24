# allOf Handling in tfmodmake

## Overview

tfmodmake implements robust `allOf` handling to correctly process Azure OpenAPI specifications that use schema composition. This document explains how `allOf` schemas are flattened and merged during code generation.

## What is allOf?

In OpenAPI/JSON Schema, `allOf` represents schema composition where an object must satisfy ALL of the listed subschemas. It's commonly used in Azure specs for:

- **Inheritance**: Base resource types extended by specific resource types
- **Composition**: Combining multiple property groups into a single schema
- **Common types**: Referencing shared schemas from external files

### Example

```json
{
  "allOf": [
    { "$ref": "#/definitions/Resource" },
    {
      "type": "object",
      "properties": {
        "properties": {
          "type": "object",
          "properties": {
            "kubernetesVersion": { "type": "string" }
          }
        }
      }
    }
  ]
}
```

## Flattening Behavior

### Property Merging

When multiple `allOf` components define properties, they are merged into a single effective schema:

```javascript
// Component 1
{ properties: { name: { type: "string" } } }

// Component 2  
{ properties: { age: { type: "integer" } } }

// Result
{ properties: { 
    name: { type: "string" },
    age: { type: "integer" }
  }
}
```

### Required Field Union

Required fields from all components are combined (union operation):

```javascript
// Component 1
{ required: ["name"] }

// Component 2
{ required: ["age", "email"] }

// Result
{ required: ["age", "email", "name"] }  // Sorted alphabetically
```

### ReadOnly Fields

Fields marked as `readOnly` are preserved in the merged schema but are excluded from Terraform input generation by the existing `isWritableProperty` checks:

```javascript
// Component with readOnly field
{
  properties: {
    id: { type: "string", readOnly: true },
    name: { type: "string" }
  },
  required: ["id", "name"]
}

// Result: Both properties present in merged schema
// Only "name" appears in generated Terraform variables
```

### Conflict Detection

If the same property name appears in multiple components with incompatible schemas, generation fails with a detailed error:

```
Error: conflicting definitions for property "count" in allOf:
component 1 defines it differently than previous definition.
First defined in schema with type=integer, description="Count as integer";
conflicting definition has type=string, description="Count as string"
```

**Equivalence checking** is tolerant of documentation differences:
- Different `description` values are OK
- Different `title` values are OK  
- Different extension values (x-ms-*) for docs are OK
- But structural differences (type, format, constraints) cause errors

### Recursive Processing

`FlattenAllOf` recursively processes:
- Nested object properties
- Array item schemas
- Additional properties schemas

This ensures deeply nested `allOf` structures are fully flattened.

### Cycle Handling

The implementation uses cache-based memoization to handle:
- Recursive structures (e.g., error details containing error details)
- Shared schemas referenced in multiple places
- Preventing infinite loops

Each schema is processed once and cached. Subsequent references return the cached result.

## Integration Points

### 1. Main Processing Pipeline

In `cmd/tfmodmake/main.go`, schemas are flattened after loading:

```go
schema, err := openapi.FindResource(doc, resourceType)
// ... apply writability overrides ...
schema, err = openapi.FlattenAllOf(schema)
```

### 2. Schema Navigation

In `openapi.NavigateSchema`, schemas are flattened at each level:

```go
for _, part := range parts {
    flattened, err := FlattenAllOf(current)
    // ... navigate to property ...
    current = prop.Value
}
```

### 3. Type Generation

The flattened schema with merged properties is used by:
- Variable generation (`generate_variables.go`)
- Locals generation (`generate_locals.go`)
- Type mapping (`mapType` function)
- Validation generation (`validations.go`)

## Testing

The implementation includes 13 comprehensive tests in `internal/openapi/allof_test.go`:

- ✅ Simple composition
- ✅ Required field merging
- ✅ ReadOnly field handling
- ✅ Equivalent properties allowed
- ✅ Conflicting properties error
- ✅ Nested allOf
- ✅ Recursive property flattening
- ✅ No allOf (passthrough)
- ✅ Nil schema handling
- ✅ Cycle detection
- ✅ Array items with allOf
- ✅ Extension merging
- ✅ Complex real-world Azure example

## Real-World Examples

### Azure AKS managedClusters

Uses `allOf` to combine:
- Base `Resource` type (id, name, type, location, tags)
- `TrackedResource` extensions
- `ManagedCluster` specific properties

After flattening, all properties are available for Terraform generation.

### Azure Container Apps managedEnvironments

Similarly uses `allOf` for composition. The flattening correctly merges common types with resource-specific properties.

## Performance

The cache-based approach ensures:
- Each schema is processed at most once
- No exponential blowup from recursive structures
- Fast lookups for shared schemas

## Future Enhancements

Potential improvements (not currently needed):
- Support for `oneOf` and `anyOf` (not commonly used in Azure specs)
- Merge validation constraints from components (currently handled in validations.go)
- Performance metrics for large specs
