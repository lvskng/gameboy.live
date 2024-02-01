FROM golang:1.22rc2-bullseye
LABEL MAINTAINER="Lovis König <lovis@outlook.com>"

#Install build dependencies
RUN apt update
RUN apt install -y libasound2-dev

WORKDIR /src
RUN git clone https://github.com/lvskng/gameboy.live.git
WORKDIR /src/gameboy.live

RUN git checkout dev
RUN git pull

RUN go get .
RUN go build -o /gbdotlive/app ./main-cli.go

RUN mkdir -p /gbdotlive/data
RUN mkdir -p /gbdotlive/ws-client
RUN mv ./config.yaml /gbdotlive/data/
RUN mv ./ws-client/ /gbdotlive/
RUN mv ./client/ /gbdotlive/

WORKDIR /gbdotlive/lib
SHELL ["/bin/bash", "-c"]
RUN bash -c "$(pkg_urls=( \
        #libasound2 libraries
        'http://deb.debian.org/debian/pool/main/a/alsa-topology-conf/alsa-topology-conf_1.2.4-1_all.deb' \
        'http://deb.debian.org/debian/pool/main/a/alsa-lib/libasound2-data_1.2.4-1.1_all.deb' \
        'http://deb.debian.org/debian/pool/main/a/alsa-lib/libasound2_1.2.4-1.1_amd64.deb' \
        'http://deb.debian.org/debian/pool/main/a/alsa-ucm-conf/alsa-ucm-conf_1.2.4-2_all.deb' \
    ); \
    for url in "${pkg_urls[@]}"; do \
        $(wget -q $url); \
    done \
)"