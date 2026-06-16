FROM golang:1.24-alpine AS src

ENV GO111MODULES=on

WORKDIR /app/
COPY . .

ARG DOCKERTAG=latest
RUN apk update; apk add git upx make tar
RUN version=`git describe --tags` && sed -i "s/NA/$version/" cmd/version.go

RUN sed -i "s/defaultDistributionTag = \"latest\"/defaultDistributionTag = \"${DOCKERTAG}\"/" ./libpvr/configurationlib.go
RUN go mod download && go mod vendor

# create vendor tarball in a separate stage so build stages don't carry it.
# Build it reproducibly: sorted entries, fixed constant mtime, fixed ownership
# and gnu format so identical vendored sources always yield the same sha256,
# independent of commit/date (Yocto recipes pin these checksums). The default
# mtime is Pantacor Ltd's incorporation date (2016-11-11 UTC = 1478822400);
# override with SOURCE_DATE_EPOCH if ever needed.
FROM src AS vendor-archive
ARG SOURCE_DATE_EPOCH
RUN LC_ALL=C tar --sort=name --format=gnu --owner=0 --group=0 --numeric-owner \
        --mtime="@${SOURCE_DATE_EPOCH:-1478822400}" \
        -czf "pvr-vendor.tar.gz" ./vendor/

# build riscv64 linux static
FROM src AS linux_riscv64

RUN make build-linux-riscv64


# build amd64 linux static
FROM src AS linux_amd64

RUN make build-linux-amd64

# build arm linux static
FROM src AS linux_arm

RUN make build-linux-arm

# build arm64 linux static
FROM src AS linux_arm64

RUN make build-linux-arm64


# build windows i386 static
FROM src AS windows_386

RUN make build-windows-386

# build windows amd64 static
FROM src AS windows_amd64

RUN make build-windows-amd64

# build darwin amd64 static
FROM src AS darwin_amd64

RUN make build-darwin-amd64


# build darwin arm64 static
FROM src AS darwin_arm64

RUN make build-darwin-arm64


FROM alpine

RUN apk update && apk add ca-certificates && rm -rf /var/cache/apk/*

WORKDIR /work
COPY --from=vendor-archive /app/pvr-vendor.tar.gz /pkg/pvr-vendor.tar.gz
COPY --from=linux_amd64 /app/build /pkg/bin
COPY --from=linux_arm /app/build /pkg/bin
COPY --from=linux_arm64 /app/build /pkg/bin
COPY --from=windows_386 /app/build /pkg/bin
COPY --from=windows_amd64 /app/build /pkg/bin
COPY --from=darwin_amd64 /app/build /pkg/bin
COPY --from=darwin_arm64 /app/build /pkg/bin

ENV USER=root

ENTRYPOINT [ "/bin/tar", "-C", "/pkg/", "-c", "." ]
