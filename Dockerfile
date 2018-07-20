FROM golang:alpine as builder

ADD . /go/src/github.com/gitprotogit/gost/

RUN go install github.com/gitprotogit/gost/cmd/gost

FROM alpine:latest

WORKDIR /bin/

COPY --from=builder /go/bin/gost .

ENTRYPOINT ["/bin/gost"]