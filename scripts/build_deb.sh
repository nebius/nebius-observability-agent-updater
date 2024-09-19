#!/bin/bash
set -x
DEB_BUILD_DIR=/tmp/nebius-observability-agent-updater

if [ -z "$NEBIUS_UPDATER_VERSION" ]; then
	NEBIUS_UPDATER_VERSION=0.0-dev
fi

rm -rf $DEB_BUILD_DIR

cp -r debian $DEB_BUILD_DIR/
mkdir -p $DEB_BUILD_DIR/usr/bin
cp nebius-observability-agent-updater $DEB_BUILD_DIR/usr/bin/nebius-observability-agent-updater
echo "Version: $NEBIUS_UPDATER_VERSION" >> $DEB_BUILD_DIR/DEBIAN/control
dpkg-deb --build $DEB_BUILD_DIR
mv /tmp/nebius-observability-agent-updater.deb nebius-observability-agent-updater-$NEBIUS_UPDATER_VERSION.deb