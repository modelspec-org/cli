# The standard forbids a dot and five reserved names in a concept name; it does
# not require identifiers, so these names are valid.
entity "Order-Item" {
  key = ["unit-price"]
  property "unit-price" {
    type = "decimal"
  }
  property "my prop" {
    type = "string"
  }
}

collection "order-items" {
  kind   = "editable"
  source = "Order-Item"
  field "created-at" {
    type = "datetime"
  }
}
