entity "Line" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "product" {
    entity = "shop.Product"
  }
}
