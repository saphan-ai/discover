.PHONY: all build test lint run
VERSION := 1.0
CC = cc

all: build

build:
	go build -o bin/tool ./cmd/tool

test:
	go test ./...

lint:
	go vet ./...

run: build
	./bin/tool

%.o: %.c
	$(CC) -c $<
