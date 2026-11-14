FROM node:22-bookworm-slim AS frontend
WORKDIR /src
COPY frontend/package*.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-bookworm AS backend
WORKDIR /src
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /server .

FROM node:22-bookworm-slim
ARG CODEX_VERSION=0.168.0
RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-venv fonts-dejavu-core ca-certificates && rm -rf /var/lib/apt/lists/*
RUN npm install -g @openai/codex@${CODEX_VERSION} && python3 -m venv /opt/venv
COPY bot/requirements.txt /app/requirements.txt
RUN /opt/venv/bin/pip install --no-cache-dir -r /app/requirements.txt
RUN groupadd -g 10001 app && useradd -m -u 10001 -g app app && mkdir -p /data /codex /ai && chown app:app /data /codex /ai
WORKDIR /app
COPY --from=backend /server /app/server
COPY --from=frontend /src/dist /app/frontend/dist
COPY bot/ /app/bot/
ENV DATA_DIR=/data FRONTEND_DIR=/app/frontend/dist AI_WORK_DIR=/ai PYTHON_BIN=/opt/venv/bin/python RENDER_SCRIPT=/app/bot/render_card.py CODEX_HOME=/codex HOME=/home/app LISTEN_ADDR=0.0.0.0:8080 BOT_DATA_DIR=/data/bot
USER app
EXPOSE 8080
CMD ["/app/server"]
