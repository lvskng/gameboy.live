#!/bin/sh
docker buildx bake bitstream-dev --set bitstream-dev.tags=lvskng/main:gbdotlive-bitstream-dev --no-cache
docker push lvskng/main:gbdotlive-bitstream-dev
