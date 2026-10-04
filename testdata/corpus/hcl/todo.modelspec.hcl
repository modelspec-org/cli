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

entity "User" {
  key = ["id"]
  use = ["Auditable"]

  property "id" {
    type = "uuid"
  }

  property "email" {
    type     = "string"
    required = true
    unique   = true
    format   = "email"
  }
}

entity "Task" {
  key = ["id"]
  use = ["Auditable"]

  property "id" {
    type = "uuid"
  }

  property "title" {
    type     = "string"
    required = true
    max_len  = 200
  }

  property "completed" {
    type     = "bool"
    required = true
  }

  property "owner" {
    entity   = "User"
    required = true
  }
}

collection "tasks" {
  kind   = "editable"
  source = "Task"

  field "id" {
    type = "uuid"
    bind = "Task.id"
  }

  field "title" {
    type = "string"
    bind = "Task.title"
  }

  field "completed" {
    type = "bool"
    bind = "Task.completed"
  }

  field "ownerId" {
    type = "uuid"
    bind = "Task.owner"
  }
}

recordset "task_summary" {
  key   = ["id"]
  query = "from tasks select id, title, completed"

  column "id" {
    type = "uuid"
    bind = "Task.id"
  }

  column "title" {
    type = "string"
    bind = "Task.title"
  }

  column "completed" {
    type = "bool"
    bind = "Task.completed"
  }
}
