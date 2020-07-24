FROM golang:1.12.5-alpine3.9 AS Server-Builder

# Add ca-certificates to get the proper certs for making requests,
# gcc and musl-dev for any cgo dependencies, and
# git for getting dependencies residing on github
RUN apk update && \
    apk add --no-cache ca-certificates gcc git musl-dev

WORKDIR /app

COPY ./vendor ./vendor

COPY ./main ./main

COPY ./go.mod ./go.sum ./

# COPY ./*.go ./

# Compile program statically with local dependencies
RUN env CGO_ENABLED=0 GO111MODULE=on GO15VENDOREXPERIMENT=1 go build -o server -ldflags '-extldflags "-static"' -tags=jsoniter -a -v -mod vendor ./main

# Last stage of build, adding in files and running
# newly compiled webserver
FROM scratch

# Copy the Go program compiled in the second stage
COPY --from=Server-Builder /app/server /

# Add HTTPS Certificates for making HTTP requests from the webserver
COPY --from=Server-Builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

EXPOSE 80
EXPOSE 443

# Run program
ENTRYPOINT ["/server"]