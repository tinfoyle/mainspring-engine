#!/bin/sh
set -eu

address="${TEMPORAL_ADDRESS:-temporal:7233}"
namespace="${DEFAULT_NAMESPACE:-default}"
host="${address%%:*}"
port="${address##*:}"

attempt=1
until nc -z -w 5 "$host" "$port"; do
    if [ "$attempt" -ge 30 ]; then
        echo "Temporal did not become reachable" >&2
        exit 1
    fi
    attempt=$((attempt + 1))
    sleep 2
done

if temporal operator namespace describe --namespace "$namespace" --address "$address" >/dev/null 2>&1; then
    exit 0
fi

temporal operator namespace create --namespace "$namespace" --address "$address"
