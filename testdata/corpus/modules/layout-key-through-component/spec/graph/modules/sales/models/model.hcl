entity "Order" {
  key = ["id"]
  use = ["core.Identified"]
}

collection "orders" {
  kind   = "editable"
  source = "Order"
  field "id" {
    type = "uuid"
    bind = "Order.id"
  }
}
