#!/bin/sh
docker buildx bake debug-remote-bitstream-dev --set debug-remote-bitstream-dev.tags=lvskng/main:gbdotlive-bitstream-debug $1;