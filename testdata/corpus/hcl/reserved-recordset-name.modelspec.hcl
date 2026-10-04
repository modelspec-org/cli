entity "User" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
}

recordset "recordsets" {
  column "id" {
    type = "uuid"
  }
}
