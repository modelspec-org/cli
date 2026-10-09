record "Order" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "customer" {
    record = "Customer"
  }
}
