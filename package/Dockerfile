FROM registry.suse.com/bci/golang:1.22 AS builder
WORKDIR /usr/src/app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN ./scripts/build

FROM registry.suse.com/bci/bci-micro:latest
COPY --from=builder /usr/src/app/bin/backup-restore-operator  /usr/bin/
ENTRYPOINT ["backup-restore-operator"]