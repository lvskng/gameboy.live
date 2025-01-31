FROM golang:1.22-bullseye AS build
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
RUN go build -o /gbdotlive/app ./main-bitstream.go

RUN mkdir -p /gbdotlive/data
RUN mv ./config.yaml /gbdotlive/data/
RUN mv ./ws-client /gbdotlive/ws-client
RUN GOOS=js GOARCH=wasm go build -o /gbdotlive/ws-client/static/main.wasm wasm/main.go

#wasm_exec.js for go 1.22
RUN wget -P /gbdotlive/ws-client/static/ https://github.com/golang/go/blob/0cc45e7ca668b103c1055ae84402ad3f3425dd56/misc/wasm/wasm_exec.js
RUN rm -rf /gbdotlive/ws-client/static/wasm

WORKDIR /gbdotlive/lib
SHELL ["/bin/bash", "-c"]
RUN bash -c 'pkg_urls=( \
        #libasound2 libraries
        "http://deb.debian.org/debian/pool/main/a/alsa-topology-conf/alsa-topology-conf_1.2.4-1_all.deb" \
        "http://deb.debian.org/debian/pool/main/a/alsa-lib/libasound2-data_1.2.4-1.1_all.deb" \
        "http://deb.debian.org/debian/pool/main/a/alsa-lib/libasound2_1.2.4-1.1_amd64.deb" \
        "http://deb.debian.org/debian/pool/main/a/alsa-ucm-conf/alsa-ucm-conf_1.2.4-2_all.deb" \
    ); \
    for url in "${pkg_urls[@]}"; do \
        wget -q "$url"; \
    done'

FROM debian:bullseye-20240130-slim
LABEL MAINTAINER="Lovis König <lovis@outlook.com>"
WORKDIR /gbdotlive
COPY --from=build /gbdotlive/ /gbdotlive/

WORKDIR /gbdotlive/lib
RUN dpkg -i *.deb

WORKDIR /gbdotlive

EXPOSE 1989
VOLUME /gbdotlive/data
CMD if ! [ -e /gbdotlive/data/rom.gb ]; then echo 'No rom.gb file found. Exiting.'; else /gbdotlive/app -b -r /gbdotlive/data/rom.gb -C /gbdotlive/data/config.yaml; fi

