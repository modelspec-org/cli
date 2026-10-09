record "User" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "email" {
    type = "string"
  }
  index "user_email_unique" {
    properties = ["email"]
    unique     = true
  }
}
