record "Thing" {
  key = ["id"]
  field "id" {
    type    = "string"
    pattern = "^[a-z]+$"
    max_len = 17
    enum    = ["a$", 5, "b%", 7]
  }
}
