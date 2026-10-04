component "Identified" {
  field "id" {
    type = "uuid"
  }
}

entity "Customer" {
  key = ["id"]
  use = ["Identified"]
}

