resource "azapi_resource" "this" {
  type                   = "test@1.0"
  name                   = var.name
  parent_id              = var.parent_id
  ignore_null_property   = true
  location               = var.location
  body                   = local.resource_body
  response_export_values = []
}
