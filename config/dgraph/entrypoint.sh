#!/bin/bash

. /run.sh &

echo "📦 Recho "🔄 Loading seed data into Dgraph..."
# Load seed data using GraphQL mutation
curl -X POST -H "Content-Type: application/json" --data-binary @/config/seed_data.graphql http://localhost:8080/graphql postCreateCommand..."

# Function to wait for a service to be healthy
wait_for_service() {
  local service_name=$1
  local url=$2

  echo "⏳ Waiting for $service_name to be ready..."
  while true; do
    health_status=$(curl -s $url)
    echo "$service_name health status: $health_status"
    if echo "$health_status" | grep -q "\"status\":\"healthy\""; then
      echo "✅ $service_name is healthy!"
      sleep 5s
      break
    fi
    sleep 1s
    echo "⏳ Still waiting for $service_name to be ready..."
  done
}

# Wait for Dgraph Alpha to be ready
wait_for_service "Dgraph Alpha" "http://localhost:8080/health"


# Load the schema into Dgraph
echo "🔄 Loading schema into Dgraph..."
curl -X POST localhost:8080/admin/schema --data-binary '@/dgraph-seed/schema.graphql'

# Wait for Dgraph to complete the schema update
sleep 5s
# Load the seed data into Dgraph
echo "🔄 Loading seed data into Dgraph..."
# Load seed data using GraphQL mutation  
# curl -X POST -H "Content-Type: application/json" -d @/dgraph-seed/seed_data.graphql http://localhost:8080/graphql

echo "✅ DevContainer setup complete!"

# Keep the container running
tail -f /dev/null