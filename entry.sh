#!/bin/sh

set -e

mkdir -p ~/.docker
jq '. + {experimental: "enabled"}' /etc/docker/config.json > ~/.docker/config.json

exec /sbin/prestomanifesto "$@"
