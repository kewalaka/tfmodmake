# Submodule Feature

This feature does the following:

- reads a supplied directory containing a terrafom sub-module
- Reads the config and extracts all variables
- in the root module (assume pwd), creates a `variables.submodule.tf` file and a `main.submodule.tf` file
- writes a new variable in the new variable file using `map(object())` type
- the above object contains all variables from the sub-module as attributes, using the same type as defined in the sub-module. Use `optional()` where applicable.
- write a new `module` block in the `main.submodule.tf` file that references the sub-module path and passes in the variable created in the variable file
- Use a for_each loop to create multiple instances of the sub-module based on the map variable

## Suggested approach

- Use `github.com/hashicorp/terraform-config-inspect/tfconfig` to read the submodule directory and extract variables
- Use `github.com/hashicorp/hcl/v2/hclwrite` to create and write the new terraform files
- Ensure proper error handling and validation throughout the process
