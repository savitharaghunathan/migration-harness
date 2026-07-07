# Migration Harness Container
# Supports Java, Python, .NET, Node.js migrations
FROM ubuntu:24.04

# Avoid interactive prompts during build
ENV DEBIAN_FRONTEND=noninteractive

# Install system dependencies
RUN apt-get update && apt-get install -y \
    # Core tools
    curl \
    wget \
    git \
    jq \
    unzip \
    ca-certificates \
    gnupg \
    software-properties-common \
    # Build essentials for compiling Python packages
    build-essential \
    gcc \
    g++ \
    make \
    # Python dependencies
    python3 \
    python3-pip \
    python3-venv \
    python3-dev \
    # Tree-sitter build dependencies (required by graphifyy)
    libffi-dev \
    pkg-config \
    # Cleanup
    && rm -rf /var/lib/apt/lists/*

# Install graphifyy (code graph generator - required for detect step)
# Note: --break-system-packages is safe in Docker (isolated environment)
RUN python3 -m pip install --break-system-packages --no-cache-dir graphifyy==0.7.17

# Install Java (OpenJDK 21)
RUN apt-get update && apt-get install -y \
    openjdk-21-jdk \
    maven \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js 20 LTS
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install .NET 8 SDK
RUN wget https://packages.microsoft.com/config/ubuntu/24.04/packages-microsoft-prod.deb -O packages-microsoft-prod.deb \
    && dpkg -i packages-microsoft-prod.deb \
    && rm packages-microsoft-prod.deb \
    && apt-get update \
    && apt-get install -y dotnet-sdk-8.0 \
    && rm -rf /var/lib/apt/lists/*

# Install Goose (Block's LLM orchestrator)
# Download binary directly to avoid interactive configuration
RUN apt-get update && apt-get install -y bzip2 \
    && mkdir -p /root/.local/bin \
    && ARCH=$(uname -m) \
    && if [ "$ARCH" = "x86_64" ]; then GOOSE_ARCH="x86_64-unknown-linux-gnu"; \
       elif [ "$ARCH" = "aarch64" ]; then GOOSE_ARCH="aarch64-unknown-linux-gnu"; \
       else echo "Unsupported architecture: $ARCH" && exit 1; fi \
    && curl -fsSL "https://github.com/block/goose/releases/download/stable/goose-${GOOSE_ARCH}.tar.bz2" -o /tmp/goose.tar.bz2 \
    && tar -xjf /tmp/goose.tar.bz2 -C /tmp \
    && mv /tmp/goose /root/.local/bin/goose \
    && chmod +x /root/.local/bin/goose \
    && rm -f /tmp/goose.tar.bz2 \
    && rm -rf /var/lib/apt/lists/*

# Set environment variables
ENV JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
ENV PATH="/root/.local/bin:${JAVA_HOME}/bin:${PATH}"
ENV PYTHONUNBUFFERED=1

# Create migration-harness installation directory
RUN mkdir -p /opt/migration-harness

# Copy migration-harness files
COPY bin/ /opt/migration-harness/bin/
COPY lib/ /opt/migration-harness/lib/
COPY skill-bundle/ /opt/migration-harness/skill-bundle/
COPY recipes/ /opt/migration-harness/recipes/
COPY install.sh /opt/migration-harness/
COPY README.md /opt/migration-harness/

# Make binaries executable
RUN chmod +x /opt/migration-harness/bin/* \
    && chmod +x /opt/migration-harness/lib/*.sh

# Create directories for config and runs
RUN mkdir -p /root/.migration-harness \
    && mkdir -p /workspace

# Add migration-harness to PATH
ENV PATH="/opt/migration-harness/bin:${PATH}"
ENV MH_INSTALL_DIR="/opt/migration-harness"

# Set working directory
WORKDIR /workspace

# Entrypoint
ENTRYPOINT ["/opt/migration-harness/bin/migration-harness"]
CMD ["--help"]
