FROM golang:1.22 AS build
WORKDIR /src
RUN go build -o /out/app ./cmd/app

# final stage
FROM --platform=linux/amd64 alpine:3.20
COPY --from=build /out/app /usr/local/bin/app
ENTRYPOINT ["/usr/local/bin/app"]
CMD ["--serve", \
     "--port", "8080"]
