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

### 1. Non-Destructive Shape Generation

**Shape consumers** (types/locals/variables) use helper functions that return effective properties and required fields without mutating the original schema:

```go
// In generate_variables.go, generate_locals.go, validations.go
effectiveProps, err := openapi.GetEffectiveProperties(schema)
effectiveRequired, err := openapi.GetEffectiveRequired(schema)
```

These functions:
- Merge properties and required fields from all `allOf` components
- Use internal caching and cycle detection
- Return errors for conflicts or cycles (treated as fatal)
- Preserve the original schema for validation generation

### 2. Constraint Generation (Validation Blocks)

**Constraint consumers** continue to use the original schema with `resolveSchemaForValidation()`:

```go
// In validations.go
childSchema := resolveSchemaForValidation(prop.Value)
```

This function applies "most restrictive wins" semantics for constraints (min/max, enum, etc.) by examining the original `allOf` array, ensuring validation blocks have correct constraint merging per PR #20.

### 3. No Global Flattening

The original schema is preserved throughout the generation pipeline:
- No global `FlattenAllOf` call in `main.go`
- No per-navigation-step flattening in `NavigateSchema`
- Properties accessed on-demand via helper functions

### 4. Legacy Compatibility

`FlattenAllOf()` is kept for backward compatibility but marked as deprecated:
- Mutates the schema graph
- No longer used in production code paths
- New code should use `GetEffectiveProperties/Required` instead

## Testing

The implementation includes comprehensive tests:

### Unit Tests for FlattenAllOf (legacy)
In `internal/openapi/allof_test.go` (13 tests):
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

### Unit Tests for GetEffectiveProperties/Required (production code path)
In `internal/openapi/allof_effective_test.go` (11 tests):
- ✅ Simple allOf composition
- ✅ No allOf (direct return)
- ✅ Conflict detection with clear errors
- ✅ Cycle detection (A→B→A)
- ✅ Nested allOf
- ✅ Required field union
- ✅ Required field deduplication
- ✅ Nested allOf for required
- ✅ Memoization across multiple references

### Integration Tests
- ✅ Real Azure AKS spec generation (uses allOf extensively)
- ✅ Real Azure Container Apps spec generation
- ✅ ReadOnly required fields excluded from Terraform variables

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
