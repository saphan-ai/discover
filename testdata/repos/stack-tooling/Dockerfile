# syntax=docker/dockerfile:1
ARG NODE_VERSION=20
FROM --platform=linux/amd64 node:${NODE_VERSION}-alpine AS build
RUN echo build
FROM golang:1.22.3@sha256:0123 AS gobuild
FROM build AS final
FROM ubuntu
FROM registry.example.invalid:5000/team/base:latest
FROM scratch
FROM python@sha256:abc123
