record "Order" {
  key = ["id"]
  field "id" {
    type = "int"
  }
  field "status" {
    type = "string"
    enum = "OrderStatus"
  }
}
