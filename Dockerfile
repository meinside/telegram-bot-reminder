# Dockerfile for Golang application

# Temporary image for building
FROM meinside/alpine-golang:latest AS builder

# Install certs
RUN apk add --no-cache ca-certificates

# Working directory outside $GOPATH
WORKDIR /src

# Copy go module files and download dependencies
COPY ./go.mod ./go.sum ./
RUN go mod download

# Copy source files
COPY ./ ./

# Build source files statically (without CGO_ENABLED=0)
RUN go build \
		-installsuffix 'static' \
		-o /app \
		.

# Minimal image for running the application
FROM alpine:latest as final

# for sqlite3 and timezone
RUN apk add --no-cache sqlite-libs tzdata

# Copy files from temporary image
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app /

# Copy config file
COPY ./config.json /

# Open ports (if needed)
#EXPOSE 8080
#EXPOSE 80
#EXPOSE 443

# Entry point for the built application
ENTRYPOINT ["/app"]
