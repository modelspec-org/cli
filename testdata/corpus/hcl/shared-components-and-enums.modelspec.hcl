# A shared module of components and enums only: no entities (decision 0014).
component "TimeWindow" {
  field "start" {
    type = "datetime"
  }
  field "end" {
    type = "datetime"
  }
}

enum "SpaceRole" {
  values = ["owner", "member"]
}
