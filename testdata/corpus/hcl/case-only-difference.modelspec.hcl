# Valid, but User and user collide on a case-insensitive store.
entity "User" {
  key = ["id"]
  property "id" {
    type = "int"
  }
  property "Name" {
    type = "string"
  }
  property "name" {
    type = "string"
  }
}

entity "user" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
