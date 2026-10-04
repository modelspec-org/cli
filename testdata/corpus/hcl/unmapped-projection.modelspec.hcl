entity "User" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
}

projection "sqlite" {
  target = "sqlite"
}
