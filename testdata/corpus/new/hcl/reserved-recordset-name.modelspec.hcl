record "User" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
}

recordset "recordsets" {
  column "id" {
    type = "uuid"
  }
}
