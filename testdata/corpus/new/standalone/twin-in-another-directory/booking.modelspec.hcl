record "Booking" {
  key = ["id"]
  field "id" {
    type = "uuid"
  }
  field "space" {
    record = "core.Space"
  }
}

