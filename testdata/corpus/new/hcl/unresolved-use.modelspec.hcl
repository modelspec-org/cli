record "Order" {
  key = ["id"]
  use = ["Auditable"]
  field "id" {
    type = "uuid"
  }
}
