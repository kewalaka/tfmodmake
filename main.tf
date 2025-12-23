resource "azapi_resource" "this" {
  type                   = "test@2025-01-01"
  name                   = var.name
  parent_id              = var.parent_id
  body                   = local.body
  response_export_values = []
}
