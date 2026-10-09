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

record "Invoice" {
  key = ["id"]
  use = ["Auditable"]

  field "id" {
    type = "uuid"
  }

  field "total" {
    component = "CurrencyAmount"
  }
}
