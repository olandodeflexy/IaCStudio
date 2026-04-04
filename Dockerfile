# ─── Stage 1: Build frontend ───
FROM node:20-alpine AS frontend
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ─── Stage 2: Build Go backend ───
FROM golang:1.22-alpine AS backend
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /iac-studio ./cmd/server

# ─── Stage 3: Runtime ───
FROM alpine:3.19
RUN apk add --no-cache ca-certificates curl git openssh-client

# Install Terraform (for running plan/apply)
ARG TERRAFORM_VERSION=1.9.0
RUN curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_amd64.zip" -o tf.zip \
    && unzip tf.zip -d /usr/local/bin/ \
    && rm tf.zip

# Install OpenTofu
ARG TOFU_VERSION=1.8.0
RUN curl -fsSL "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_linux_amd64.zip" -o tofu.zip \
    && unzip tofu.zip -d /usr/local/bin/ \
    && rm tofu.zip

WORKDIR /app
COPY --from=backend /iac-studio /app/iac-studio

# Default project directory
RUN mkdir -p /projects /data

ENV IAC_STUDIO_PROJECTS_DIR=/projects

EXPOSE 3000

ENTRYPOINT ["/app/iac-studio"]
CMD ["--host", "0.0.0.0", "--port", "3000", "--projects-dir", "/projects"]
