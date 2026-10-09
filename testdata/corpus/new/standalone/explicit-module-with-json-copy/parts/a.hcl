record "Product" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "kind" {
    type = "string"
    enum = "Kind"
  }
}
