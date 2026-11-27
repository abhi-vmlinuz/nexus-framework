# Slim Kali container image for Nexus AttackBox (nexus-role=env).
# Intentionally omits systemd, sshd, and code-server; nexus-engine runs
# `sleep infinity` and attaches an interactive bash PTY via kubelet exec.
FROM kalilinux/kali-rolling:latest

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    wget \
    netcat-openbsd \
    nmap \
    dnsutils \
    iputils-ping \
    iproute2 \
    openssh-client \
    python3 \
    python3-pip \
    python3-requests \
    gcc \
    gdb \
    binutils \
    file \
    jq \
    git \
    nano \
    vim-tiny \
    unzip \
    sqlmap \
    gobuster \
    john \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash kali && mkdir -p /workspace && chown kali:kali /workspace
WORKDIR /workspace

CMD ["sleep", "infinity"]
