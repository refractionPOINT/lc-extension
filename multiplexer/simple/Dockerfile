FROM golang:1.24 AS builder

ENV GOPATH=

WORKDIR /build
ADD . /build
RUN cd /build && go build -v -o simple

FROM gcr.io/distroless/base-debian12:latest

COPY --from=builder /build/simple /simple

CMD ["/simple"]
