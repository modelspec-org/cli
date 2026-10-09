record "User" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
}

projection "sqlite" {
  target = "sqlite"
}
