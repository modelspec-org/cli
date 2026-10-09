record "Invoice" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "total" {
    component = "Money"
  }
  field "state" {
    type = "string"
    enum = "State"
  }
}
