entity "Order" {
  key = ["id"]
  property "id" {
    type = "int"
  }
  property "status" {
    type = "string"
    enum = "OrderStatus"
  }
}
