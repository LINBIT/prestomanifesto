FROM golang:alpine AS builder
WORKDIR /go/src/prestomanifesto
COPY . .

RUN go build && mv ./prestomanifesto /

FROM alpine:latest
MAINTAINER Roland Kammerer <roland.kammerer@linbit.com>

COPY --from=builder /prestomanifesto /sbin
RUN apk update \
	&& apk add ca-certificates docker jq \
	&& rm -rf /var/cache/apk/*

ADD ./entry.sh /

CMD ["-h"]
ENTRYPOINT ["/entry.sh"]
