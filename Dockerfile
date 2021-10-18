# syntax=docker/dockerfile:1
##
## Build
##
FROM golang:1.17-buster AS build

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY *.go ./
RUN go build -o ./ekspose

##
## Deploy
##
FROM golang:1.17-buster
WORKDIR /app
COPY --from=build /app/ekspose ./
ENTRYPOINT ["/app/ekspose"]