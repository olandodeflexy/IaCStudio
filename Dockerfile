# ─── Stage 1: Build frontend ───
FROM node:20-alpine AS frontend
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ─── Stage 2: Build Go backend ───
FROM golang:1.25-alpine AS backend
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/dist ./cmd/server/frontend/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /iac-studio ./cmd/server

# ─── Stage 3: Runtime ───
FROM alpine:3.19
RUN apk add --no-cache ca-certificates curl git openssh-client unzip

# Install Terraform (for running plan/apply) — with checksum verification
ARG TARGETARCH=amd64
ARG TERRAFORM_VERSION=1.9.0
RUN set -eux; \
    case "${TARGETARCH}" in amd64|arm64) terraform_arch="${TARGETARCH}" ;; *) echo "unsupported Terraform arch: ${TARGETARCH}" >&2; exit 1 ;; esac; \
    terraform_zip="terraform_${TERRAFORM_VERSION}_linux_${terraform_arch}.zip"; \
    curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/${terraform_zip}" -o "${terraform_zip}"; \
    curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_SHA256SUMS" -o terraform.sha256; \
    grep " ${terraform_zip}$" terraform.sha256 | sha256sum -c -; \
    unzip "${terraform_zip}" -d /usr/local/bin/; \
    rm "${terraform_zip}" terraform.sha256

# Install OpenTofu — with checksum verification
ARG TOFU_VERSION=1.8.0
RUN set -eux; \
    case "${TARGETARCH}" in amd64|arm64) tofu_arch="${TARGETARCH}" ;; *) echo "unsupported OpenTofu arch: ${TARGETARCH}" >&2; exit 1 ;; esac; \
    tofu_zip="tofu_${TOFU_VERSION}_linux_${tofu_arch}.zip"; \
    curl -fsSL "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/${tofu_zip}" -o "${tofu_zip}"; \
    curl -fsSL "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_SHA256SUMS" -o tofu.sha256; \
    grep " ${tofu_zip}$" tofu.sha256 | sha256sum -c -; \
    unzip "${tofu_zip}" -d /usr/local/bin/; \
    rm "${tofu_zip}" tofu.sha256

WORKDIR /app
COPY --from=backend /iac-studio /app/iac-studio

# Default project directory
RUN mkdir -p /projects /data

ENV IAC_STUDIO_PROJECTS_DIR=/projects

EXPOSE 3000

ENTRYPOINT ["/app/iac-studio"]
CMD ["--host", "127.0.0.1", "--port", "3000", "--projects-dir", "/projects"]
