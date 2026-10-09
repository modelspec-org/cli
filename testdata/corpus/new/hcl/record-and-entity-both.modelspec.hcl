# A member that carries both record and entity is an error: it refers to one record.
record "Customer" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
}

record "Order" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "customer" {
    record = "Customer"
    entity = "Customer"
  }
}
