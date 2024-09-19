#!/bin/bash
set -ex

UPDATER_ENDPOINT_PATH=/tmp/updater-endpoint #FIXME
NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT=$(cat $UPDATER_ENDPOINT_PATH)
export NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT
echo 'running nebius-observability-agent-updater with endpoint' "$NEBIUS_OBSERVABILITY_AGENT_UPDATER_ENDPOINT"

exec /usr/bin/nebius-observability-agent-updater --config /etc/nebius-observability-agent-updater/config.yaml
