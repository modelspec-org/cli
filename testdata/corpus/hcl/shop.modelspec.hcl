# A model that uses named enums, components, a module-qualified self reference,
# a collection and a recordset.
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

entity "Customer" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "email" {
    type   = "string"
    unique = true
    format = "email"
  }
}

entity "Order" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "customer" {
    entity   = "shop.Customer"
    required = true
  }
  property "status" {
    type = "string"
    enum = "OrderStatus"
  }
  property "size" {
    type = "string"
    enum = ["S", "M", "L"]
  }
  property "total" {
    component = "Money"
  }
}

collection "orders" {
  kind   = "editable"
  source = "Order"
  field "id" {
    type = "uuid"
    bind = "Order.id"
  }
}

collection "recent_orders" {
  kind  = "computed"
  query = "from orders where recent"
}

recordset "order_totals" {
  key = ["id"]
  column "id" {
    type = "uuid"
    bind = "Order.id"
  }
  column "total" {
    type   = "decimal"
    source = "sum(orders.total)"
  }
  column "total" {
    type = "decimal"
  }
}
