record "Line" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "product" {
    record = "shop.Product"
  }
}
