#!/bin/sh
docker buildx bake bitstream-dev --set bitstream-dev.tags=lvskng/main:gbdotlive-bitstream-dev $1;
docker push lvskng/main:gbdotlive-bitstream-dev
