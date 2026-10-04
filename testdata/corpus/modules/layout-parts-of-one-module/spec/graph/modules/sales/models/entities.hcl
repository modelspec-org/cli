entity "Invoice" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "total" {
    component = "Money"
  }
  property "state" {
    type = "string"
    enum = "State"
  }
}
