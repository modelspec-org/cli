# Decision 0013: an enum constrains a string or an int property.
enum "Priority" {
  values = [1, 2, 3]
}

entity "Task" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "priority" {
    type = "int"
    enum = "Priority"
  }
  property "size" {
    type = "int"
    enum = [10, 20]
  }
}
