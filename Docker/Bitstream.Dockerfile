FROM debian:bullseye-20240130-slim
LABEL MAINTAINER="Lovis König <lovis@outlook.com>"
WORKDIR /gbdotlive
COPY --from=build /gbdotlive/ /gbdotlive/

WORKDIR /gbdotlive/lib
RUN dpkg -i *.deb

EXPOSE 1989
VOLUME /gbdotlive/data
CMD if ! [ -e /gbdotlive/data/rom.gb ]; then echo 'No rom.gb file found. Exiting.'; else /gbdotlive/app -l -r /gbdotlive/data/rom.gb; fi
