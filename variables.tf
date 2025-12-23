variable "name" {
  description = <<DESCRIPTION
The name of the resource.
DESCRIPTION
  type        = string
}

variable "parent_id" {
  description = <<DESCRIPTION
The parent resource ID for this resource.
DESCRIPTION
  type        = string
}

variable "location" {
  description = <<DESCRIPTION
The location of the resource.
DESCRIPTION
  type        = string
}

variable "count" {
  description = <<DESCRIPTION
The count of the resource.
DESCRIPTION
  type        = number
  default     = null
  validation {
    condition     = var.count  ==  null  ||  var.count  >=  1
    error_message = "count must be greater than or equal to 1."
  }
  validation {
    condition     = var.count  ==  null  ||  var.count  <=  100
    error_message = "count must be less than or equal to 100."
  }
}

variable "display_name" {
  description = <<DESCRIPTION
The displayName of the resource.
DESCRIPTION
  type        = string
  default     = null
  validation {
    condition     =var.display_name  ==  null  ||  length(var.display_name)  >=  3
    error_message = "display_name must have a minimum length of 3."
  }
  validation {
    condition     =var.display_name  ==  null  ||  length(var.display_name)  <=  50
    error_message = "display_name must have a maximum length of 50."
  }
}

variable "percentage" {
  description = <<DESCRIPTION
The percentage of the resource.
DESCRIPTION
  type        = number
  default     = null
  validation {
    condition     = var.percentage  ==  null  ||  var.percentage  >=  0
    error_message = "percentage must be greater than or equal to 0."
  }
  validation {
    condition     = var.percentage  ==  null  ||  var.percentage  <=  100
    error_message = "percentage must be less than or equal to 100."
  }
}

variable "tags" {
  description = <<DESCRIPTION
The tags of the resource.
DESCRIPTION
  type        = list(string)
  default     = null
  validation {
    condition     =var.tags  ==  null  ||  length(var.tags)  >=  1
    error_message = "tags must have at least 1 item(s)."
  }
  validation {
    condition     =var.tags  ==  null  ||  length(var.tags)  <=  10
    error_message = "tags must have at most 10 item(s)."
  }
}

