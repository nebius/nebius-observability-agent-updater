#!/bin/bash
set -ex
UPDATER_ENDPOINT=observability-agent-manager.api.nebius.cloud

UPDATER_ENDPOINT_PATH_OVERRIDE=/mnt/cloud-metadata/updater-endpoint-override
if [ -f $UPDATER_ENDPOINT_PATH_OVERRIDE ]; then
    UPDATER_ENDPOINT=$(cat $UPDATER_ENDPOINT_PATH_OVERRIDE)
fi

export GOMAXPROCS=1

NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT=$UPDATER_ENDPOINT
export NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT
echo 'running nebius-observability-agent-updater with endpoint' "$NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT"

exec /usr/bin/nebius-observability-agent-updater --config /etc/nebius-observability-agent-updater/config.yaml
