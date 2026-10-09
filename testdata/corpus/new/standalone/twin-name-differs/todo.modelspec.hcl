record "Task" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "parent" {
    record = "todo.Task"
  }
}
