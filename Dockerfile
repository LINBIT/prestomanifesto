FROM golang:alpine AS builder
WORKDIR /go/src/prestomanifesto
COPY . .

ENV CGO_ENABLED=0
RUN go build && mv ./prestomanifesto /

FROM gcr.io/go-containerregistry/crane:debug
MAINTAINER Roland Kammerer <roland.kammerer@linbit.com>

COPY --from=builder /prestomanifesto /sbin
ENTRYPOINT ["/sbin/prestomanifesto"]
CMD ["-h"]
