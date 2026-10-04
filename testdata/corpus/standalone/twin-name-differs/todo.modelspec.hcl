entity "Task" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "parent" {
    entity = "todo.Task"
  }
}
