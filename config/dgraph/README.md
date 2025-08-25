# Dgraph Config

Development configuration for Dgraph graph database used by DeepFry subsystems.

## Overview

Single-node Dgraph deployment (Zero + Alpha in one container) for development. Stores graph relationships only—canonical Nostr events remain in StrFry.

## Services

- **Dgraph**: `http://localhost:8080` (GraphQL API + Admin)
- **Dgraph Ratel UI**: `http://localhost:8000` (Visual query interface)

## Schema

Web-of-trust focused schema for Nostr pubkey relationships:

```graphql
type User {
  pubkey: String! @search(by: [exact])
  name: String @search(by: [fulltext])
  about: String
  follows: [User]
  reports: [Report]
  labels: [Label]
}

type Report {
  reason: String
  reportedUser: User!
  reportingUser: User!
}

type Label {
  value: String!
  labeledUser: User!
  labelingUser: User!
}
```

## Common Operations

### GraphQL Mutations (Port 8080)

**Add User:**

```graphql
mutation {
  addUser(
    input: [{ pubkey: "npub1...", name: "Alice", about: "Nostr enthusiast" }]
  ) {
    user {
      pubkey
      name
    }
  }
}
```

**Add Follow Relationship:**

```graphql
mutation {
  updateUser(input: {
    filter: { pubkey: { eq: "npub1alice..." } }
    set: {
      follows: [{ pubkey: "npub1bob..." }]
```
