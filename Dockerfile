FROM golang:1.13.5-alpine as builder

COPY . /go/src/github.com/kolbyjack/docker-volume-s3fs
WORKDIR /go/src/github.com/kolbyjack/docker-volume-s3fs

RUN set -ex \
    && apk add --no-cache gcc libc-dev \
    && go install --ldflags '-extldflags "-static"' \
CMD ["/go/bin/docker-volume-s3fs"]

FROM alpine
RUN set -ex \
    && echo @testing https://dl-cdn.alpinelinux.org/alpine/edge/testing >> /etc/apk/repositories
    && apk add --update tini s3fs-fuse@testing
    && mkdir -p /run/docker/plugins /mnt/state /mnt/volumes
COPY --from=builder /go/bin/docker-volume-s3fs /
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["docker-volume-s3fs"]
