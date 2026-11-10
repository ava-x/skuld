FROM golang:alpine3.19 AS build

RUN apk update; apk add git openssh-client

# Trust github.com's host keys so that the SSH clone in compile.sh can run non-interactively.
RUN mkdir -p -m 0700 /root/.ssh && ssh-keyscan github.com >> /root/.ssh/known_hosts

WORKDIR /workdir
COPY build/compile.sh /workdir
# Forward the host's ssh-agent (docker build --ssh default) to clone the private repo.
RUN --mount=type=ssh /workdir/compile.sh

FROM alpine:3.19.1

ENV PATH=/usr/local/bin:$PATH
COPY --from=build /dist/* /usr/local/bin/
