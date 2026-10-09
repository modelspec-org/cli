# A model that uses named enums, components and a module-qualified self reference.
enum "OrderStatus" {
  values = ["open", "paid", "shipped"]
}

component "Money" {
  field "amount" {
    type     = "decimal"
    required = true
  }
  field "currency" {
    type     = "string"
    max_len  = 3
  }
}

record "Customer" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "email" {
    type   = "string"
    unique = true
    format = "email"
  }
}

record "Order" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "customer" {
    record   = "shop.Customer"
    required = true
  }
  field "status" {
    type = "string"
    enum = "OrderStatus"
  }
  field "size" {
    type = "string"
    enum = ["S", "M", "L"]
  }
  field "total" {
    component = "Money"
  }
}
