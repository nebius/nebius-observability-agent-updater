#!/bin/bash
set -ex
listFIle=$(grep -rl "https://dr.nebius.cloud/" /etc/apt/sources.list.d |head | sed s'|/etc/apt/||g')
sudo apt update -o Dir::Etc::sourcelist="$listFIle" \
                -o Dir::Etc::sourceparts="-" \
                -o APT::Get::List-Cleanup="0"
