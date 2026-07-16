FROM rancher/hardened-build-base:v1.25.12b1 AS builder

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
