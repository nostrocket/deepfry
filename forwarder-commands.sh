#!/bin/bash
# Quick reference commands for managing event forwarders

# ============================================================================
# SETUP
# ============================================================================

# 1. Copy .env.example to .env and configure
cp .env.example .env

# 2. Generate secret keys
echo "LIVE KEY:"
openssl rand -hex 32
echo ""
echo "HISTORY KEY:"
openssl rand -hex 32

# 3. Edit .env and add the keys above
# nano .env

# ============================================================================
# START/STOP
# ============================================================================

# Start main infrastructure first
docker-compose up -d

# Start all forwarders
docker-compose -f docker-compose.evtfwd.yml up -d

# Start specific forwarder
docker-compose -f docker-compose.evtfwd.yml up -d fwd-damus-live

# Stop all forwarders
docker-compose -f docker-compose.evtfwd.yml down

# Stop without removing
docker-compose -f docker-compose.evtfwd.yml stop

# Restart all
docker-compose -f docker-compose.evtfwd.yml restart

# Restart specific
docker-compose -f docker-compose.evtfwd.yml restart fwd-damus-live

# ============================================================================
# MONITORING
# ============================================================================

# View all logs
docker-compose -f docker-compose.evtfwd.yml logs -f

# View specific forwarder
docker-compose -f docker-compose.evtfwd.yml logs -f fwd-damus-live

# View last 100 lines
docker-compose -f docker-compose.evtfwd.yml logs --tail=100

# Check status
docker-compose -f docker-compose.evtfwd.yml ps

# Check resource usage
docker stats $(docker ps --filter name=fwd- -q)

# Check restart counts
for container in $(docker ps --filter name=fwd- --format "{{.Names}}"); do
  echo "$container: $(docker inspect $container --format='{{.RestartCount}}')"
done

# ============================================================================
# DEBUGGING
# ============================================================================

# Check environment variables
docker inspect fwd-damus-live | grep -A 30 Env

# Check network connectivity
docker run --rm --network deepfry-net alpine/curl curl -v ws://strfry:7777

# Test if strfry is accessible
docker ps | grep strfry
docker logs --tail=50 strfry

# View detailed container info
docker inspect fwd-damus-live

# ============================================================================
# UPDATES
# ============================================================================

# Rebuild images
docker-compose -f docker-compose.evtfwd.yml build

# Rebuild specific service
docker-compose -f docker-compose.evtfwd.yml build fwd-damus-live

# Update and restart
docker-compose -f docker-compose.evtfwd.yml build
docker-compose -f docker-compose.evtfwd.yml up -d

# Force recreate containers
docker-compose -f docker-compose.evtfwd.yml up -d --force-recreate

# ============================================================================
# CLEANUP
# ============================================================================

# Stop and remove all forwarders
docker-compose -f docker-compose.evtfwd.yml down

# Remove including volumes
docker-compose -f docker-compose.evtfwd.yml down -v

# Remove old/unused images
docker image prune -a

# ============================================================================
# INDIVIDUAL FORWARDER MANAGEMENT
# ============================================================================

# Live forwarders
docker logs -f fwd-damus-live
docker logs -f fwd-primal-live
docker logs -f fwd-nostr-lol-live

# Historical forwarders
docker logs -f fwd-damus-history
docker logs -f fwd-primal-history
docker logs -f fwd-nostr-lol-history

# Restart individual forwarders
docker restart fwd-damus-live
docker restart fwd-damus-history

# Stop individual forwarders
docker stop fwd-damus-live
docker stop fwd-damus-history

# Start individual forwarders
docker start fwd-damus-live
docker start fwd-damus-history

# ============================================================================
# HEALTH CHECKS
# ============================================================================

# Check if all forwarders are running
docker ps --filter name=fwd- --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# Count running forwarders (should be 6)
docker ps --filter name=fwd- -q | wc -l

# Check for errors in logs
docker-compose -f docker-compose.evtfwd.yml logs | grep -i error

# Check memory usage per forwarder
docker stats --no-stream --format "table {{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}" $(docker ps --filter name=fwd- -q)

# ============================================================================
# NETWORK DEBUGGING
# ============================================================================

# List networks
docker network ls

# Inspect deepfry-net
docker network inspect deepfry-net

# Check which containers are on deepfry-net
docker network inspect deepfry-net --format='{{range .Containers}}{{.Name}} {{end}}'

# Test connectivity between containers
docker exec fwd-damus-live ping -c 3 strfry
