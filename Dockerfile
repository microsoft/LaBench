FROM golang:alpine as builder
RUN mkdir /builder
RUN apk add --no-cache git
ADD . /build/
WORKDIR /build
RUN go build
FROM alpine
COPY --from=builder /build/labench /app/
COPY labench.yaml /app/
WORKDIR /app
CMD ["./labench"]