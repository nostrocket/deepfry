# Whitelist / StrFry plugin config

These YAML files are **per-host deploy config** and are **gitignored** so the checkout
stays clean and `git pull` never conflicts. Only the `*.example` templates are tracked.

## First-time setup on a host

```bash
cd config/whitelist
cp whitelist.yaml.example        whitelist.yaml          # writePolicy plugins (whitelist/router/bloom)
cp router.yaml.example           router.yaml             # router plugin
cp whitelist-server.yaml.example whitelist-server.yaml   # whitelist server (cmd/server)
# then edit each for this host (see notes below)
```

The real files are read by viper from `~/deepfry/whitelist.yaml` inside each container —
the compose mounts rename the host file into place (e.g. `whitelist-server.yaml` →
`/root/deepfry/whitelist.yaml` in the server container).

## What to change per host

| File | Key | Single-host (shared docker net) | Split deploy (server on another LAN host) |
|------|-----|---------------------------------|-------------------------------------------|
| `whitelist.yaml` / `router.yaml` | `server_url` | `http://whitelist-server:8081` | `http://<server-ip>:8081` |
| `whitelist-server.yaml` | `dgraph_graphql_url` | `http://dgraph:8080/graphql` | `http://<dgraph-ip>:8080/graphql` |
| `whitelist-server.yaml` | `debug` | `true` (dev) | `false` (prod) |

Docker **network** name is set separately via `.env` (`DEEPFRY_NET_NAME`) — see the
root `.env.example`. Together these mean a host needs **no tracked-file edits**: all
host-specific values live in gitignored `*.yaml` + `.env`.
