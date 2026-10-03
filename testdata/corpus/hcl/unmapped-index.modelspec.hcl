entity "User" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "email" {
    type = "string"
  }
  index "user_email_unique" {
    properties = ["email"]
    unique     = true
  }
}
