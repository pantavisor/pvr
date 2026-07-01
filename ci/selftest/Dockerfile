# Clean environment to exercise the real install + self-upgrade paths, exactly
# like a user machine with nothing pre-installed.
FROM ubuntu:22.04
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl tar gzip ca-certificates python3 \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /root
