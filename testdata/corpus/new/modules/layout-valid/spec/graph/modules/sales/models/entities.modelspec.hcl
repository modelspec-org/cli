record "Order" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "status" {
    type   = "string"
    enum   = "OrderStatus"
  }
  field "customer" {
    record = "core.Customer"
  }
}

