# The Invoice example of spec/core-model.md: a component-valued property.
component "Auditable" {
  field "createdAt" {
    type     = "datetime"
    required = true
  }
}

component "CurrencyAmount" {
  field "amount" {
    type     = "decimal"
    required = true
  }
  field "currency" {
    type     = "string"
    required = true
  }
}

entity "Invoice" {
  key = ["id"]
  use = ["Auditable"]

  property "id" {
    type = "uuid"
  }

  property "total" {
    component = "CurrencyAmount"
  }
}
