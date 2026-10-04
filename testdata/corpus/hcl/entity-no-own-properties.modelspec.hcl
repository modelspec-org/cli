component "Identified" {
  field "id" {
    type = "uuid"
  }
}

entity "Tag" {
  key = ["id"]
  use = ["Identified"]
}
