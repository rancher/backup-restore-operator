FROM registry.suse.com/bci/bci-base:15.7 AS zypper

RUN mkdir /chroot
RUN zypper --non-interactive refresh && \
    zypper --non-interactive --installroot /chroot install git grep

FROM dp.apps.rancher.io/containers/go:1.26.8 AS builder

ARG TAG=''
ARG REPO=''
ENV TAG=$TAG REPO=$REPO

COPY --from=zypper /chroot /

WORKDIR /usr/src/app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN ./scripts/build

FROM registry.suse.com/bci/bci-base:15.7 AS user-setup
RUN useradd -u 1000 -U -m -s /sbin/nologin bro
RUN mkdir /encryption

FROM registry.suse.com/bci/bci-nano:15.7
COPY --from=builder /usr/src/app/bin/backup-restore-operator /usr/bin/
COPY --from=user-setup /etc/passwd /etc/passwd
COPY --from=user-setup /etc/group /etc/group
COPY --from=user-setup /etc/shadow /etc/shadow
COPY --from=user-setup --chown=1000:1000 /home/bro /home/bro
COPY --from=user-setup --chown=1000:1000 /encryption /encryption

USER 1000

ENTRYPOINT ["backup-restore-operator"]
