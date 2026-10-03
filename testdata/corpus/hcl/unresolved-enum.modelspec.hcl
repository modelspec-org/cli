entity "Order" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "status" {
    type = "string"
    enum = "OrderStatus"
  }
}
