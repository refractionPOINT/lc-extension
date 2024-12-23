FROM golang AS builder

ENV GOPATH=

WORKDIR /
ADD . .
RUN go build -v

FROM gcr.io/distroless/base-debian12:latest

COPY --from=builder /simple /simple

CMD ["/simple"]
