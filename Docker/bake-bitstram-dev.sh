#!/bin/sh
if [$1 -eq "git"];
    docker buildx bake build-bitstream-dev $2
else
    docker buildx bake bitstream-dev --set bitstream-dev.tags=lvskng/main:gbdotlive-bitstream-dev $1;
fi
docker push lvskng/main:gbdotlive-bitstream-dev
