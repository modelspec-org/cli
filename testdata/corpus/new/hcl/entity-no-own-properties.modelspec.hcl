component "Identified" {
  field "id" {
    type = "uuid"
  }
}

record "Tag" {
  key = ["id"]
  use = ["Identified"]
}
