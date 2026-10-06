FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/mailbridge .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 1000 mailer
COPY --from=build /out/mailbridge /usr/local/bin/mailbridge
USER mailer
WORKDIR /app
EXPOSE 6245
ENTRYPOINT ["mailbridge"]
