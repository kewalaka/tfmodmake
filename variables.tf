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

variable "port_start" {
  description = <<DESCRIPTION
Port (1-65535).
DESCRIPTION
  type        = number
  default     = null
  validation {
    condition = (
            var.port_start  ==  null  ||
          (
              var.port_start  >=  1  &&
              var.port_start  <=  65535
          )
      )
    error_message = "port_start must be >= 1 and <= 65535."
  }
}

