# Build from the repository root so multiplexer's `replace ../` resolves:
#   docker build -f multiplexer/simple/Dockerfile .
FROM golang:1.25 AS builder

ENV GOPATH=

WORKDIR /build
ADD . /build
RUN cd /build/multiplexer && go build -v -o /build/simple ./simple

FROM gcr.io/distroless/base-debian12:latest

COPY --from=builder /build/simple /simple

CMD ["/simple"]
