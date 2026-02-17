#!/bin/bash
set -ex
UPDATER_ENDPOINT=observability-agent-manager.api.nebius.cloud

METADATA_BASE_URL="http://metadata.nebius.internal"
METADATA_FALLBACK_URL="http://169.254.169.254"
METADATA_HEADER="Metadata: true"

# Try to get updater endpoint override from IMDS
OVERRIDE=$(curl -s -f -H "$METADATA_HEADER" "${METADATA_BASE_URL}/v1/instance-data/o11y/updater-endpoint-override" 2>/dev/null || \
           curl -s -f -H "$METADATA_HEADER" "${METADATA_FALLBACK_URL}/v1/instance-data/o11y/updater-endpoint-override" 2>/dev/null || true)
if [ -n "$OVERRIDE" ]; then
    UPDATER_ENDPOINT="$OVERRIDE"
fi

export GOMAXPROCS=1

NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT=$UPDATER_ENDPOINT
export NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT
echo 'running nebius-observability-agent-updater with endpoint' "$NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT"

exec /usr/bin/nebius-observability-agent-updater --config /etc/nebius-observability-agent-updater/config.yaml
