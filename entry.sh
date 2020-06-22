#!/bin/sh

set -e

force=n

args=""
for o in "$@"; do
	if [ "$o" = '--force' ]; then
		force=y
		continue
	fi

	args="$args $o"
done

set -- "$args"

mkdir -p ~/.docker
jq '. + {experimental: "enabled"}' /etc/docker/config.json > ~/.docker/config.json

/sbin/prestomanifesto "$*" | tee /tmp/presto.sh
if [ "$force" = 'y' ]; then
	sh /tmp/presto.sh
fi
