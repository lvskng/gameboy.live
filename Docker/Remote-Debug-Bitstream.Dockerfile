FROM debian:bullseye-20240130-slim
LABEL MAINTAINER="Lovis König <lovis@outlook.com>"
WORKDIR /gbdotlive
COPY --from=build /gbdotlive/ /gbdotlive/
COPY --from=build /go/bin/dlv /dlv

WORKDIR /gbdotlive/lib
RUN dpkg -i *.deb

WORKDIR /gbdotlive

EXPOSE 1989
VOLUME /gbdotlive/data
CMD if ! [ -e /gbdotlive/data/rom.gb ]; then echo 'No rom.gb file found. Exiting.'; else /dlv --listen=:2345 --headless=true --log=true --log-output=debugger,debuglineerr,gdbwire,lldbout,rpc --accept-multiclient --api-version=2 exec /gbdotlive/app -b -r /gbdotlive/data/rom.gb -C /gbdotlive/data/config.yaml; fi
