# Event Forwarder Deployment Checklist

Use this checklist to ensure proper deployment of the event-forwarder services.

## Pre-Deployment

- [ ] **Main infrastructure is running**

  ```bash
  docker-compose ps
  # Should show: strfry, dgraph, dgraph-ratel all running
  ```

- [ ] **deepfry-net network exists**

  ```bash
  docker network ls | grep deepfry-net
  ```

- [ ] **Event forwarder Docker image is built**
  ```bash
  cd event-forwarder
  make docker-build
  docker images | grep deepfry/event-forwarder
  ```

## Configuration

- [ ] **Generate secret keys**

  ```bash
  # Run twice to get two different keys
  openssl rand -hex 32
  ```

  - Key 1 (LIVE): `_________________________________`
  - Key 2 (HISTORY): `_________________________________`

- [ ] **Create .env file**

  ```bash
  cp .env.example .env
  ```

- [ ] **Add keys to .env**

  - [ ] `NOSTR_SYNC_SECKEY_LIVE=<key1>`
  - [ ] `NOSTR_SYNC_SECKEY_HISTORY=<key2>`
  - [ ] `STRFRY_PRIVATE_KEY=<if not already set>`

- [ ] **Validate .env is gitignored**

  ```bash
  git status .env
  # Should show: nothing (file is ignored)
  ```

- [ ] **Validate docker-compose syntax**
  ```bash
  docker-compose -f docker-compose.evtfwd.yml config
  # Should output valid YAML (warnings about blank env vars are OK)
  ```

## Deployment

- [ ] **Build forwarder images**

  ```bash
  docker-compose -f docker-compose.evtfwd.yml build
  ```

- [ ] **Start forwarders**

  ```bash
  docker-compose -f docker-compose.evtfwd.yml up -d
  ```

- [ ] **Verify all 6 containers started**
  ```bash
  docker-compose -f docker-compose.evtfwd.yml ps
  # Should show 6 services: 3 live + 3 history
  ```

## Validation

- [ ] **Check container status**

  ```bash
  docker ps --filter name=fwd-
  # All should show status: Up
  ```

- [ ] **Check logs for errors**

  ```bash
  docker-compose -f docker-compose.evtfwd.yml logs --tail=50
  # Look for connection success, no errors
  ```

- [ ] **Verify connectivity to strfry**

  ```bash
  # Each forwarder should show: "Connected to DeepFry relay"
  docker logs fwd-damus-live 2>&1 | grep -i connect
  ```

- [ ] **Verify sync started**

  ```bash
  # Should see: "Starting sync" or "Events: received=X"
  docker logs fwd-damus-live --tail=20
  ```

- [ ] **Check resource usage**
  ```bash
  docker stats --no-stream $(docker ps --filter name=fwd- -q)
  # Memory should be <64 MB per container typically
  ```

## Live Forwarders Check

- [ ] **fwd-damus-live**

  ```bash
  docker logs fwd-damus-live --tail=10
  # Should show recent activity, no errors
  ```

- [ ] **fwd-primal-live**

  ```bash
  docker logs fwd-primal-live --tail=10
  ```

- [ ] **fwd-nostr-lol-live**
  ```bash
  docker logs fwd-nostr-lol-live --tail=10
  ```

## Historical Forwarders Check

- [ ] **fwd-damus-history**

  ```bash
  docker logs fwd-damus-history --tail=10
  # Should show: "Sync start time: 2020-01-01T00:00:00Z"
  ```

- [ ] **fwd-primal-history**

  ```bash
  docker logs fwd-primal-history --tail=10
  ```

- [ ] **fwd-nostr-lol-history**
  ```bash
  docker logs fwd-nostr-lol-history --tail=10
  ```

## Post-Deployment Monitoring (first hour)

- [ ] **Monitor event rates**

  ```bash
  # Check every 5-10 minutes for first hour
  docker-compose -f docker-compose.evtfwd.yml logs --tail=30 | grep "Events:"
  ```

- [ ] **Check for restarts**

  ```bash
  for container in $(docker ps --filter name=fwd- --format "{{.Names}}"); do
    echo "$container: $(docker inspect $container --format='{{.RestartCount}}')"
  done
  # All should show: 0 restarts
  ```

- [ ] **Verify sync progress in strfry**

  ```bash
  # Query strfry for kind 30078 events (sync progress)
  # These should be published by forwarders
  docker exec strfry /app/strfry export | grep '"kind":30078'
  ```

- [ ] **Monitor memory trends**
  ```bash
  # Run a few times over the first hour
  docker stats --no-stream --format "table {{.Name}}\t{{.MemUsage}}" \
    $(docker ps --filter name=fwd- -q)
  ```

## Troubleshooting (if needed)

- [ ] **Forwarder won't start**

  - [ ] Check logs: `docker logs fwd-<name>`
  - [ ] Verify .env has required keys
  - [ ] Ensure strfry is running
  - [ ] Check network: `docker network inspect deepfry-net`

- [ ] **Can't connect to source relay**

  - [ ] Check relay URL is correct
  - [ ] Test relay externally: `wscat -c wss://relay.damus.io`
  - [ ] Check network/firewall settings

- [ ] **Can't connect to strfry**

  - [ ] Verify strfry is on deepfry-net
  - [ ] Test internal DNS: `docker exec fwd-damus-live ping strfry`
  - [ ] Check strfry logs: `docker logs strfry`

- [ ] **High memory usage**

  - [ ] Check relay activity level
  - [ ] Review batch sizes in config
  - [ ] Consider adjusting resource limits

- [ ] **Frequent restarts**
  - [ ] Check exit reason: `docker inspect fwd-<name> --format='{{.State}}'`
  - [ ] Review recent logs for errors
  - [ ] Verify network stability

## Ongoing Monitoring

Set up regular checks (daily/weekly):

- [ ] **Log review** (daily)

  ```bash
  docker-compose -f docker-compose.evtfwd.yml logs --since 24h | grep -i error
  ```

- [ ] **Resource usage** (daily)

  ```bash
  docker stats --no-stream $(docker ps --filter name=fwd- -q)
  ```

- [ ] **Restart counts** (weekly)

  ```bash
  for container in $(docker ps --filter name=fwd- --format "{{.Names}}"); do
    echo "$container: $(docker inspect $container --format='{{.RestartCount}}')"
  done
  ```

- [ ] **Disk usage** (weekly)
  ```bash
  du -sh data/strfry-db
  docker system df
  ```

## Success Criteria

✅ Deployment is successful when:

1. All 6 forwarder containers are running (Up status)
2. No error messages in logs
3. Event rates showing activity (received/forwarded counts increasing)
4. Memory usage stable (<64 MB per container)
5. No container restarts (restart count = 0)
6. Connections to source and DeepFry relays established
7. Sync progress events visible in strfry (kind 30078)

## Notes

- Live forwarders: Should start showing events within 30 seconds
- Historical forwarders: May take 1-2 minutes to start processing (fetching from 2020)
- First sync window may be slower as state is initialized
- Expect higher CPU during initial catchup for historical forwarders
- Memory should stabilize after first few minutes

## Support Resources

- [FORWARDER_DEPLOYMENT.md](FORWARDER_DEPLOYMENT.md) - Full guide
- [forwarder-commands.sh](forwarder-commands.sh) - Command reference
- [event-forwarder/README.md](event-forwarder/README.md) - App docs
- [event-forwarder/DOCKER.md](event-forwarder/DOCKER.md) - Docker guide

---

**Date**: ******\_\_\_******
**Deployed by**: ******\_\_\_******
**Environment**: [ ] Dev [ ] Staging [ ] Production
