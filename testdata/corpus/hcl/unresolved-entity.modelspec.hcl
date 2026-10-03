entity "Order" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "customer" {
    entity = "Customer"
  }
}
