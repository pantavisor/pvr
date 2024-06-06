FROM golang:1.21.5-alpine as src

ENV GO111MODULES=on

WORKDIR /app/
COPY . .

ARG DOCKERTAG=latest
RUN apk update; apk add git upx make
RUN version=`git describe --tags` && sed -i "s/NA/$version/" version.go
RUN sed -i "s/defaultDistributionTag = \"develop\"/defaultDistributionTag = \"${DOCKERTAG}\"/" ./libpvr/configurationlib.go
RUN go get

# build riscv64 linux static
FROM src as linux_riscv64

RUN apk update; apk add git
RUN make build-linux-riscv64


# build amd64 linux static
FROM src as linux_amd64

RUN apk update; apk add git
RUN make build-linux-amd64

# build arm linux static
FROM src as linux_arm

RUN apk update; apk add git
RUN make build-linux-arm

# build arm64 linux static
FROM src as linux_arm64

RUN apk update; apk add git
RUN make build-linux-arm64


# build windows i386 static
FROM src as windows_386

RUN apk update; apk add git
RUN make build-windows-386

# build windows amd64 static
FROM src as windows_amd64

RUN apk update; apk add git
RUN make build-windows-amd64

# build darwin amd64 static
FROM src as darwin_amd64

RUN apk update; apk add git
RUN make build-darwin-amd64


# build darwin arm64 static
FROM src as darwin_arm64

RUN apk update; apk add git
RUN make build-darwin-arm64


FROM alpine

RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*

WORKDIR /work
COPY --from=linux_amd64 /app/build /pkg/bin
COPY --from=linux_arm /app/build /pkg/bin
COPY --from=linux_arm64 /app/build /pkg/bin
COPY --from=windows_386 /app/build /pkg/bin
COPY --from=windows_amd64 /app/build /pkg/bin
COPY --from=darwin_amd64 /app/build /pkg/bin
COPY --from=darwin_arm64 /app/build /pkg/bin

ENV USER root

ENTRYPOINT [ "/bin/tar", "-C", "/pkg/", "-c", "." ]

