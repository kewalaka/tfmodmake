locals {
  body = {
    properties = var.port_start  ==  null  ?  null  :  {
      portStart = var.port_start
    }
  }
}
