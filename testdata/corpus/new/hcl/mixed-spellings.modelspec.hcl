# Old and new spellings may be mixed in one file: either member word in either
# block spelling, and either spelling of the reference setting.
record "Customer" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
}

entity "Order" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "customer" {
    entity = "Customer"
  }
  property "shipTo" {
    record = "Customer"
  }
}
