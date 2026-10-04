# The migration block of spec/migration-metadata.md.
entity "User" {
  key = ["id"]
  property "id" {
    type = "uuid"
  }
  property "displayName" {
    type = "string"
  }
}

migration "2026-07-08-user-display-name" {
  from = "1.2.0"
  to   = "1.3.0"

  rename "User.fullName" {
    to = "User.displayName"
  }

  backfill "User.displayName" {
    strategy = "copy"
    from     = "User.fullName"
  }
}
