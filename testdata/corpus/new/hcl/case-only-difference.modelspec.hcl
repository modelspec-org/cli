# Valid, but User and user collide on a case-insensitive store.
record "User" {
  key = ["id"]
  field "id" {
    type = "int"
  }
  field "Name" {
    type = "string"
  }
  field "name" {
    type = "string"
  }
}

record "user" {
  key = ["id"]
  field "id" {
    type = "int"
  }
}
