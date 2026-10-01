FROM node:22-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends git ripgrep ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force
COPY server.mjs ./
ENV HOME=/workspace/home CLAUDE_CONFIG_DIR=/workspace/home/.claude
EXPOSE 80
CMD ["node", "/app/server.mjs"]
