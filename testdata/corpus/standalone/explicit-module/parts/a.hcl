entity "Product" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "kind" {
    type = "string"
    enum = "Kind"
  }
}
