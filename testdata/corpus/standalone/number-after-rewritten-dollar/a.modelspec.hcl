entity "Thing" {
  key = ["id"]
  property "id" {
    type    = "string"
    pattern = "^[a-z]+$"
    max_len = 17
    enum    = ["a$", 5, "b%", 7]
  }
}
