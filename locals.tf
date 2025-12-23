locals {
  resource_body = {
    location = var.location
    properties = {
      count       = var.count
      displayName = var.display_name
      percentage  = var.percentage
      tags        = var.tags == null ? null : [for item in var.tags : item]
    }
  }
}
