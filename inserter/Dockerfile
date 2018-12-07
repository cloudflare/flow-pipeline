ARG src_dir="/go/src/github.com/cloudflare/flow-pipeline/inserter"

FROM golang:alpine as builder
ARG src_dir

RUN apk --update --no-cache add git && \
    mkdir -p ${src_dir}

WORKDIR ${src_dir}
COPY . .

RUN go get -u github.com/golang/dep/cmd/dep && \
    dep ensure && \
    go build

FROM alpine:latest
ARG src_dir

RUN apk update --no-cache && \
    adduser -S -D -H -h / flow
USER flow
COPY --from=builder ${src_dir}/inserter /

ENTRYPOINT ["./inserter"]
