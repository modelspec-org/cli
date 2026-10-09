# The standard forbids a dot and the reserved kind names in a concept name; it does
# not require identifiers, so these names are valid.
record "Order-Item" {
  key = ["unit-price"]
  field "unit-price" {
    type = "decimal"
  }
  field "my prop" {
    type = "string"
  }
}
