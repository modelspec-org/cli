entity "Order" {
  key = ["id"]
  use = ["Auditable"]
  property "id" {
    type = "uuid"
  }
}
