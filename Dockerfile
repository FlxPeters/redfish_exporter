# This is the original Dockerfile that was used to build the redfish_exporter image
# Currently we use gorelaser to build the binary
# FROM golang:1.21-alpine AS builder

# WORKDIR /src
# COPY . .
# RUN go mod download
# RUN go build -o /build/redfish_exporter

FROM scratch

COPY redfish_exporter /redfish_exporter
COPY config.example.yml redfish_exporter.yml
CMD ["/redfish_exporter","--config.file","/redfish_exporter.yml"]
EXPOSE 9610
