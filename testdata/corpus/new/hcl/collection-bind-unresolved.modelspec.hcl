record "Task" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
}

collection "tasks" {
  kind   = "editable"
  source = "Task"
  field "title" {
    type = "string"
    bind = "Task.title"
  }
}
