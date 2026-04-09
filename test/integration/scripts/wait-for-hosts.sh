#!/bin/bash
# Wait for all target hosts to be ready

set -e

HOSTS=("172.28.0.10" "172.28.0.11" "172.28.0.12")
PORT=22
TIMEOUT=120
INTERVAL=2

echo "Waiting for target hosts to be ready..."

for host in "${HOSTS[@]}"; do
    elapsed=0
    while true; do
        if nc -z "$host" "$PORT" 2>/dev/null; then
            echo "Host $host is ready"
            break
        fi

        if [ $elapsed -ge $TIMEOUT ]; then
            echo "ERROR: Timeout waiting for host $host"
            exit 1
        fi

        echo "Waiting for $host..."
        sleep $INTERVAL
        elapsed=$((elapsed + INTERVAL))
    done
done

echo "All hosts are ready!"

