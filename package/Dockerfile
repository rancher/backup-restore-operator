FROM registry.suse.com/bci/golang:1.23 AS builder

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

RUN echo 'bro:x:1000:1000::/home/bro:/bin/bash' >> /etc/passwd && \
    echo 'bro:x:1000:' >> /etc/group && \
    mkdir /home/bro && \
    chown -R bro:bro /home/bro
COPY --from=builder /usr/src/app/bin/backup-restore-operator  /usr/bin/
USER bro

ENTRYPOINT ["backup-restore-operator"]