# Agent image: everything Claude needs to build, test and push. Build:  docker build -t nightshift-agent .
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod env.example.json ./
COPY *.go ./
COPY prompts ./prompts
RUN CGO_ENABLED=0 go build -o /out/nightshift .

FROM mcr.microsoft.com/dotnet/sdk:9.0
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends git curl ca-certificates gnupg ripgrep jq \
 && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli.gpg \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
 && apt-get update && apt-get install -y --no-install-recommends gh \
 && rm -rf /var/lib/apt/lists/* \
 && npm install -g @anthropic-ai/claude-code
COPY --from=golang:1.24 /usr/local/go /usr/local/go
COPY --from=build /out/nightshift /usr/local/bin/nightshift
COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN sed -i 's/\r$//' /usr/local/bin/entrypoint.sh && chmod +x /usr/local/bin/entrypoint.sh \
 && useradd -m -s /bin/bash agent \
 && mkdir -p /work /home/agent/.claude && chown -R agent /work /home/agent
ENV PATH="/usr/local/go/bin:/home/agent/go/bin:${PATH}" DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1
USER agent
WORKDIR /work
ENTRYPOINT ["entrypoint.sh"]
