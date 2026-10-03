enum "Status" {
  values = ["a"]
}

entity "Task" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "status" {
    entity = "Status"
  }
}
