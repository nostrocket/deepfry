workspace "DeepFry C4" "DeepFry backend for Humble Horse — StrFry relay plus subsystems for search, trust, profiles, threads." {

  model {
    user = person "User" "Interacts via Humble Horse client"
    hhc  = softwareSystem "Humble Horse Client" "Mobile and web client"
    upstream = softwareSystem "Upstream Relays" "External Nostr relays"

    deepfry = softwareSystem "DeepFry" "Relay + backend subsystems" {
      strfry = container "StrFry Relay" "Canonical Nostr relay. Stores and serves events. Dispatches NIP-50 to plugin." "C++"
      lmdb   = container "LMDB" "Canonical full Nostr events." "Embedded DB" {
        tags "Database"
      }
      plugin = container "Search Plugin" "Intercepts NIP-50 and calls Semantic Search." "StrFry plugin (stdin/stdout JSON)"
      search = container "Semantic Search Service" "Vector search API: /index, /search. Hybrid rank." "TS/Go API"
      vecdb  = container "Vector DB" "Embeddings and metadata." "pgvector" {
        tags "Database"
      }
      embed  = container "Embeddings Generator" "Clean, chunk, embed, upsert to Vector DB." "Go workers"
      forwarder = container "Event Forwarder" "stream/sync from upstream relays; republish to StrFry; offset as replaceable event." "Service"
      wot    = container "WoT Builder + Crawler" "Compute trust scores; export allowlist." "Go/.NET"
      dgraph = container "Dgraph" "ID-only graphs: pubkeys and event-ids." "Graph DB" {
        tags "Database"
      }
      profile = container "Profile Builder" "Track Likes, Mutes, Replies, Reposts, Zaps; compute interests." "Service"
      thread  = container "Thread Inference Engine" "Build thread graph from reply/quote links." "Go/.NET"
    }

    user -> hhc      "Reads and publishes Nostr events"
    hhc  -> deepfry  "WebSocket"
    upstream -> deepfry "events via stream/sync"

    strfry -> lmdb    "Store/query full events"
    strfry -> plugin  "Dispatch NIP-50"
    plugin -> search  "HTTP /search, stream results"
    search -> vecdb   "kNN/ANN queries"
    embed  -> search  "HTTP /index upserts"
    strfry -> embed   "Event Kind 1"
    forwarder -> strfry "Republish EVENTs"
    strfry -> wot     "NIP-02 contacts"
    strfry -> profile "Kinds 1/18/25/51/57"
    strfry -> thread  "Kinds 1 with e/p tags"
    wot    -> dgraph  "Pubkey trust graph"
    thread -> dgraph  "Event-id thread graph"
    profile -> vecdb "profile data (tba)"

    deploymentEnvironment "Logical" {
      deploymentNode "User Device" {
        softwareSystemInstance hhc
      }
      deploymentNode "Edge / LB" "TCP/WebSocket" { }
      deploymentNode "Relay Host" {
        containerInstance strfry
        containerInstance plugin
        containerInstance lmdb
      }
      deploymentNode "Search Tier" {
        containerInstance search
        containerInstance vecdb
        containerInstance embed
      }
      deploymentNode "Graph Tier" {
        containerInstance dgraph
      }
      deploymentNode "Services" {
        containerInstance forwarder
        containerInstance profile
        containerInstance thread
      }
      deploymentNode "Upstream Relays" {
        softwareSystemInstance upstream
      }
    }
  }

  views {
    systemContext deepfry "context" {
      include *
      autoLayout lr
    }

    container deepfry "containers" {
      include *
      autoLayout lr
    }

    deployment deepfry "Logical" "deployment-logical" {
      include *
      autoLayout lr
    }

    styles {
      element "Person" {
        background #08427b
        color #ffffff
        shape Person
      }
      element "Software System" {
        background #1168bd
        color #ffffff
      }
      element "Container" {
        background #438dd5
        color #ffffff
      }
      element "Database" {
        shape Cylinder
      }
    }
  }
}
