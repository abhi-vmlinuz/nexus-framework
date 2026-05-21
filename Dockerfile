FROM rust:1.85-slim

# Install system dependencies
RUN apt-get update && apt-get install -y \
    curl \
    git \
    wget \
    xz-utils \
    protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

# Install Go 1.24
ENV GO_VERSION=1.24.0
RUN curl -sSL https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz | tar -v -C /usr/local -xz
ENV PATH=$PATH:/usr/local/go/bin

# Install Zig
ENV ZIG_VERSION=0.12.0
RUN wget -q https://ziglang.org/download/${ZIG_VERSION}/zig-linux-x86_64-${ZIG_VERSION}.tar.xz \
    && tar -xf zig-linux-x86_64-${ZIG_VERSION}.tar.xz \
    && mv zig-linux-x86_64-${ZIG_VERSION} /usr/local/zig \
    && rm zig-linux-x86_64-${ZIG_VERSION}.tar.xz
ENV PATH=$PATH:/usr/local/zig

# Add Rust MUSL targets for cross-compilation
RUN rustup target add x86_64-unknown-linux-musl aarch64-unknown-linux-musl

# Install cargo-zigbuild
RUN cargo install cargo-zigbuild --version 0.21.8 --locked

WORKDIR /workspace
