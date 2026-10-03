entity "Task" {
  key = ["id"]
  property "id" {
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
