enum "Status" {
  values = ["a"]
}

record "Task" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "status" {
    record = "Status"
  }
}
