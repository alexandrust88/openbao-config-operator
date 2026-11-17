# renovate: datasource=docker depName=alpine/git
FROM alpine/git:2.52.0@sha256:4a0e72d49596a1f5d3701aeedafdadc5c0da4062be4657c7bdc4017387f591cc
RUN apk add --no-cache --upgrade git-daemon=2.52.1-r0
COPY server /server
USER 1000:1000
ENTRYPOINT ["/server"]
