FROM golang:alpine

RUN apk add rclone

RUN adduser user --disabled-password 
RUN addgroup user root

COPY ./src /src

ENTRYPOINT go run /src/main.go
