FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /snmp-exporter .

FROM alpine:3.20
COPY --from=build /snmp-exporter /usr/local/bin/snmp-exporter
EXPOSE 9161
ENTRYPOINT ["snmp-exporter"]
