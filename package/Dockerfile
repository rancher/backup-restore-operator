ARG GO_IMAGE=registry.suse.com/rancher/appco-go:1.26.11
ARG BCI_VERSION=16.0
ARG FINAL_IMAGE=registry.suse.com/bci/bci-nano:${BCI_VERSION}

FROM --platform=$BUILDPLATFORM rancher/mirrored-tonistiigi-xx:1.6.1 AS xx

FROM --platform=$BUILDPLATFORM registry.suse.com/bci/bci-base:${BCI_VERSION} AS zypper
RUN mkdir /chroot
COPY --from=xx / /chroot
ARG TARGETPLATFORM
RUN zypper -n refresh
RUN zypper -n --installroot /chroot install --no-recommends git grep util-linux file
RUN zypper --installroot /chroot clean -a
RUN rm -rf /chroot/var/cache/zypp/* /chroot/var/log/zypp/* /chroot/etc/zypp/ /chroot/run/zypp.pid

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder
ARG TARGETPLATFORM
ARG TARGETARCH
# This doesn't work.
# COPY --from=zypper /chroot /
# Please check: https://superuser.com/questions/1828813/docker-error-failed-to-solve-cannot-copy-to-non-directory-when-copying-to
COPY --from=xx / /
COPY --from=zypper /chroot/bin /bin
COPY --from=zypper /chroot/lib /lib
COPY --from=zypper /chroot/lib64 /lib64
COPY --from=zypper /chroot/usr /usr

ARG TAG=''
ARG REPO=''
ENV TAG=$TAG REPO=$REPO

WORKDIR /usr/src/app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOARCH=${TARGETARCH} ARCH=${TARGETARCH} ./scripts/build

RUN xx-verify /usr/src/app/bin/backup-restore-operator

FROM registry.suse.com/bci/bci-base:${BCI_VERSION} AS user-setup
RUN useradd -u 1000 -U -m -s /sbin/nologin bro
RUN mkdir /encryption && chown -R 1000:1000 /encryption

FROM ${FINAL_IMAGE} AS final
COPY --from=builder /usr/src/app/bin/backup-restore-operator /usr/bin/
COPY --from=user-setup /etc/passwd /etc/passwd
COPY --from=user-setup /etc/group /etc/group
COPY --from=user-setup /etc/shadow /etc/shadow
COPY --from=user-setup --chown=1000:1000 /encryption /encryption
COPY --from=user-setup --chown=1000:1000 /home/bro /home/bro

USER 1000

ENTRYPOINT ["backup-restore-operator"]
