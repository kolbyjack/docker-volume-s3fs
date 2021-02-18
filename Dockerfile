FROM golang:1.13.5-alpine as builder

COPY . /go/src/github.com/kolbyjack/docker-volume-s3fs
WORKDIR /go/src/github.com/kolbyjack/docker-volume-s3fs

RUN set -ex \
    && apk add --no-cache --virtual .build-deps \
    gcc libc-dev \
    && go install --ldflags '-extldflags "-static"' \
    && apk del .build-deps
CMD ["/go/bin/docker-volume-s3fs"]

FROM alpine
RUN echo @testing https://dl-cdn.alpinelinux.org/alpine/edge/testing >> /etc/apk/repositories
RUN apk add --update s3fs-fuse@testing
RUN mkdir -p /run/docker/plugins /mnt/state /mnt/volumes
COPY --from=builder /go/bin/docker-volume-s3fs .
# Tini to reap orphaned child procceses
# Add Tini
RUN apk add --update tini
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["docker-volume-s3fs"]
