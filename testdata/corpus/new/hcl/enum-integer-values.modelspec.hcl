# Decision 0013: an enum constrains a string or an int property.
enum "Priority" {
  values = [1, 2, 3]
}

record "Task" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "priority" {
    type = "int"
    enum = "Priority"
  }
  field "size" {
    type = "int"
    enum = [10, 20]
  }
}
