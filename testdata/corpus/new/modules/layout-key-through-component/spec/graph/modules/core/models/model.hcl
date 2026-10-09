component "Identified" {
  field "id" {
    type = "uuid"
  }
}

record "Customer" {
  key = ["id"]
  use = ["Identified"]
}
