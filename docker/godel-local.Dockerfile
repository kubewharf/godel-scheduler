FROM debian:stable-slim

WORKDIR /root
COPY bin/linux_amd64/* /usr/local/bin/
