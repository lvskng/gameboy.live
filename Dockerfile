FROM golang:latest as build
MAINTAINER Lovis König <lovis@outlook.com>

WORKDIR /src
RUN git clone https://github.com/lvskng/gameboy.live.git

#Install build dependencies
RUN apt update
RUN apt install -y libasound2-dev
WORKDIR /src/gameboy.live

RUN go get .
RUN go build -o /gbdotlive/app ./main-cli.go

FROM debian:bookworm-slim as final
WORKDIR /gbdotlive
COPY --from=build /gbdotlive/app /gbdotlive/app
RUN apt update
RUN apt install -y libasound2
EXPOSE 1989
VOLUME /gbdotlive/rom
CMD if ! [ -e ./rom/rom.gb ]; then echo 'No rom.gb file found. Exiting.'; else /gbdotlive/app -S -r ./rom/rom.gb; fi