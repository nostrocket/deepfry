#!/bin/bash

# Start Dgraph (Zero + Alpha) in the background
/run.sh &

# Function to wait for a service to be healthy
wait_for_service() {
  local service_name=$1
  local url=$2

  echo "⏳ Waiting for $service_name to be ready..."
  while true; do
    health_status=$(curl -s $url)
    if echo "$health_status" | grep -q "\"status\":\"healthy\""; then
      echo "✅ $service_name is healthy!"
      sleep 5s
      break
    fi
    sleep 1s
  done
}

# Wait for Dgraph Alpha to be ready
wait_for_service "Dgraph Alpha" "http://localhost:8080/health"

# Load the schema into Dgraph
echo "🔄 Loading schema into Dgraph..."
curl -X POST localhost:8080/admin/schema --data-binary '@/dgraph-seed/schema.graphql'

# Wait for Dgraph to complete the schema update
sleep 5s

echo "✅ Dgraph setup complete!"

# Keep the container running
tail -f /dev/null
