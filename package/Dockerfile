ARG GO_IMAGE=rancher/hardened-build-base:v1.26.6b1
FROM ${GO_IMAGE} AS builder

ARG TAG=''
ARG REPO=''
ENV TAG=$TAG REPO=$REPO

WORKDIR /usr/src/app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN ./scripts/build

FROM registry.suse.com/bci/bci-micro:latest
COPY --from=builder /usr/src/app/bin/backup-restore-operator  /usr/bin/
ENTRYPOINT ["backup-restore-operator"]
