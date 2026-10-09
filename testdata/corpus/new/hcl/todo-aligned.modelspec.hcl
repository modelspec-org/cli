component "Auditable" {
  field "createdAt" {
    type     = "datetime"
    required = true
  }

  field "updatedAt" {
    type     = "datetime"
    required = true
  }
}

record "User" {
  key = ["id"]
  use = ["Auditable"]

  field "id" {
    type = "uuid"
  }

  field "email" {
    type     = "string"
    required = true
    unique   = true
    format   = "email"
  }
}

record "Task" {
  key = ["id"]
  use = ["Auditable"]

  field "id" {
    type = "uuid"
  }

  field "title" {
    type     = "string"
    required = true
    max_len  = 200
  }

  field "completed" {
    type     = "bool"
    required = true
  }

  field "owner" {
    record   = "User"
    required = true
  }
}
