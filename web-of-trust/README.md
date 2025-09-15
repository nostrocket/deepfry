# Web of Trust Builder + Crawler

This subsystem builds a trust graph from NIP-02 follows.

_Placeholder README. More details coming soon._

## Querying the Graph

You can explore the Web of Trust data using Ratel (Dgraph's UI) at http://localhost:8000 (if running Dgraph locally).

### Sample Queries

See `queries/explore.dql` for a collection of useful queries including:

- View all users with follow/follower counts
- Find specific users and their connections
- Identify mutual follows
- Explore follow graphs at various depths
- Find popular users or isolated nodes

### Quick Test Query

To verify data is being stored correctly, run this in Ratel:

```graphql
{
  test(func: has(pubkey), first: 5) {
    uid
    pubkey
    kind3CreatedAt
    last_db_update
    follows_count: count(follows)
    followers_count: count(~follows)
  }
}
```

This will show the first 5 pubkeys with their follow relationships.
